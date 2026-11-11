package agentd

// Persisted sandbox workspaces (ADR-20261027090013 step 7). A persist
// reaches agentd on the session's stream: agentd reserves the archive's
// bytes against the account's SandboxSnapshotBytesPerAccount and grants vmmd
// a key, then records the written archive as ready. Its sweep deletes
// archives past SandboxSnapshotRetentionDays, ones apid was asked to delete,
// and reservations vmmd never reported within SandboxSnapshotPendingSec.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
	"github.com/onebox-faas/faas/pkg/storage"
)

// snapshotSweepBatch bounds the snapshots one sweep deletes.
const snapshotSweepBatch = 100

// grantPersist answers a persist request with a reservation or a refusal.
func (s *Sandboxes) grantPersist(ctx context.Context, ss *session, requestID string, send func(*scheddpb.SandboxRunRequest) error) {
	grant := &scheddpb.SandboxRunPersistGrant{RequestId: requestID}
	snap, problem := s.reserveSnapshot(ctx, ss)
	if problem != nil {
		refusal, err := json.Marshal(problem)
		if err != nil {
			return
		}
		grant.Refusal = refusal
	} else {
		ss.mu.Lock()
		ss.persists[requestID] = snap.ID
		ss.mu.Unlock()
		grant.SnapshotId, grant.StorageKey, grant.MaxBytes = snap.ID, snap.StorageKey, snap.ReservedBytes
		grant.ExpiresAtUnixMs = snap.ExpiresAt.UnixMilli()
	}
	if err := send(&scheddpb.SandboxRunRequest{Frame: &scheddpb.SandboxRunRequest_PersistGrant{PersistGrant: grant}}); err != nil {
		s.log.Warn("agentd: send persist grant failed", "sandbox_id", ss.claim.ID, "err", err)
	}
}

// reserveSnapshot reserves room for one archive: the plan's
// SandboxWorkspaceMaxMB, within what is left of its
// SandboxSnapshotBytesPerAccount.
func (s *Sandboxes) reserveSnapshot(ctx context.Context, ss *session) (agentstore.SandboxSnapshot, *api.Problem) {
	plan := api.Plan(ss.claim.Plan)
	idx := plan.PlanIndex()
	if s.scfg.Workspaces == nil {
		return agentstore.SandboxSnapshot{}, api.ErrSandboxUnsupported("workspace:persist", "persisted workspaces are not enabled here yet")
	}
	quota := api.SandboxSnapshotBytesPerAccount[idx]
	if quota == 0 {
		return agentstore.SandboxSnapshot{}, api.ErrAgentLimit(plan, "SandboxSnapshotBytesPerAccount", 0, 1)
	}
	snap, err := s.store.ReserveSandboxSnapshot(ctx, agentstore.SnapshotReservation{SnapshotID: uuid.NewString(),
		AccountID: ss.claim.AccountID, SandboxID: ss.claim.ID, MaxBytes: int64(api.SandboxWorkspaceMaxMB[idx]) << 20,
		QuotaBytes: quota, ExpiresAt: s.now().Add(time.Duration(api.SandboxSnapshotRetentionDays[idx]) * 24 * time.Hour)})
	var limit *agentstore.LimitError
	switch {
	case errors.As(err, &limit):
		return agentstore.SandboxSnapshot{}, api.ErrAgentLimit(plan, limit.Limit, limit.Max, limit.Observed)
	case err != nil:
		s.log.Warn("agentd: reserve sandbox snapshot failed", "sandbox_id", ss.claim.ID, "err", err)
		return agentstore.SandboxSnapshot{}, api.NewProblem(http.StatusServiceUnavailable, api.CodeSandboxWorkspaceUnavailable,
			"Workspace not persisted", "the snapshot could not be reserved; persist again")
	}
	return snap, nil
}

// recordPersist records a written archive as ready, or drops the
// reservation of one that failed, and answers vmmd.
func (s *Sandboxes) recordPersist(ctx context.Context, ss *session, done *scheddpb.SandboxRunPersistDone, send func(*scheddpb.SandboxRunRequest) error) {
	ss.mu.Lock()
	id, ok := ss.persists[done.GetRequestId()]
	delete(ss.persists, done.GetRequestId())
	ss.mu.Unlock()
	recorded := false
	switch {
	case !ok || id != done.GetSnapshotId():
		// Not a reservation of this session's: nothing to record.
	case done.GetFailed():
		if err := s.store.DeleteSandboxSnapshot(ctx, id); err != nil {
			s.log.Warn("agentd: drop sandbox snapshot failed", "sandbox_id", ss.claim.ID, "snapshot_id", id, "err", err)
		}
	default:
		err := s.store.CompleteSandboxSnapshot(ctx, id, done.GetBytes(), done.GetSha256())
		if err != nil {
			s.log.Warn("agentd: record sandbox snapshot failed", "sandbox_id", ss.claim.ID, "snapshot_id", id, "err", err)
		} else {
			recorded = true
			s.log.Info("agentd: sandbox workspace persisted", "sandbox_id", ss.claim.ID, "snapshot_id", id, "bytes", done.GetBytes())
		}
	}
	answer := &scheddpb.SandboxRunPersistRecorded{RequestId: done.GetRequestId(), Recorded: recorded}
	if err := send(&scheddpb.SandboxRunRequest{Frame: &scheddpb.SandboxRunRequest_PersistRecorded{PersistRecorded: answer}}); err != nil {
		s.log.Warn("agentd: send persist record failed", "sandbox_id", ss.claim.ID, "err", err)
	}
}

// sweepSnapshots deletes the archives and rows of snapshots past their
// retention, asked to be deleted, or never completed. An archive that cannot
// be deleted keeps its row, so the next sweep tries again.
func (s *Sandboxes) sweepSnapshots(ctx context.Context) error {
	if s.scfg.Workspaces == nil {
		return nil
	}
	due, err := s.store.SweepableSandboxSnapshots(ctx, time.Duration(api.SandboxSnapshotPendingSec)*time.Second, snapshotSweepBatch)
	if err != nil {
		return err
	}
	var errs []error
	for _, snap := range due {
		if err := s.scfg.Workspaces.Delete(ctx, snap.StorageKey); err != nil && !errors.Is(err, storage.ErrNotFound) {
			errs = append(errs, err)
			continue
		}
		if err := s.store.DeleteSandboxSnapshot(ctx, snap.ID); err != nil {
			errs = append(errs, err)
		}
	}
	if n := len(due) - len(errs); n > 0 {
		s.log.Info("agentd: deleted sandbox snapshots", "count", n)
	}
	return errors.Join(errs...)
}
