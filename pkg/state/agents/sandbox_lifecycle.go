package agents

// Sandbox session lifecycle (ADR-20261027090013 §1, ADR-20261027090014):
// the transitions agentd owns. A replica claims a queued session under a
// lease, as it claims runs, and the session counts against the account's
// agent concurrency from then until it ends.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/agents/sqlc"
)

// SandboxClaim is a session a replica holds.
type SandboxClaim struct {
	ID        string
	AccountID string
	Plan      string
	RAMMB     int
	// Network is the session's network mode (ADR-20261027090013 §6).
	Network string
	// WorkspaceKey is the archive of the persisted workspace the session
	// starts from; WorkspaceMissing marks one whose snapshot is gone.
	WorkspaceKey     string
	WorkspaceMissing bool
	// ImageKey is the seller image's artifact (drive1) in the artifact
	// store; ImageMissing marks a session from an image that is no longer
	// ready. A preset session has neither.
	ImageKey     string
	ImageMissing bool
	// StartCmd, ReadyCmd and ReadyTimeout are a template session's
	// commands; empty for any other session.
	StartCmd     string
	ReadyCmd     string
	ReadyTimeout time.Duration
	// EgressAllow and EgressDeny are the session's web-mode domain policy
	// (ADR-20261117091026 §3); empty is none.
	EgressAllow []string
	EgressDeny  []string
	// Browser boots a preset session with the sandbox browser running
	// (ADR-20261116182615 §1).
	Browser bool
	// Task is the session's sandbox task, if it is one
	// (ADR-20261116182615 §3).
	Task *SandboxTask
	// Kind is SandboxEphemeral or SandboxPersistent; VCPU is a persistent
	// session's vCPUs, 0 for the plan's default (ADR-20261122132055 §5).
	Kind string
	VCPU int
	// Resume is the capture a paused persistent session resumes from, when
	// the claim resumed one (ADR-20261122132055 §2). A resume without a live
	// capture has Resuming set and Resume nil: it cold-boots.
	Resuming bool
	Resume   *SandboxCapture
	// VolumeGB is a persistent session's workspace volume size (0: none)
	// and VolumeBackupID its newest backup (ADR-20261122132055 §4).
	VolumeGB       int
	VolumeBackupID string
	// VolumeNodeID holds the volume's newest copy; VolumeStoredBackupID is
	// its newest backup the shared store holds.
	VolumeNodeID         string
	VolumeStoredBackupID string
	// ForkOf is a fork's source session (ADR-20261122132055 §3): Resume is
	// the source's capture, which the shared store holds, and the volume
	// restores from the source's backup VolumeBackupID.
	ForkOf     string
	LeaseToken string
	SandboxLease
}

// SandboxCapture is a persistent session's pause capture
// (ADR-20261122132055 §2): its objects, the Firecracker version and size,
// and the node it was taken on.
type SandboxCapture struct {
	ID                string
	NodeID            string
	StorageKey        string
	VMStateStorageKey string
	FCVersion         string
	MemBytes          int64
	VMStateBytes      int64
	// HasBacking: the capture's backing identity was written (ADR-510).
	HasBacking bool
	// Remote: the shared store holds the whole capture (durability
	// remote), so any node can restore it; otherwise only NodeID can.
	Remote bool
	// SessionID and AccountID are the capture's session, in a durability
	// check.
	SessionID string
	AccountID string
	// CreatedAt is when the capture was recorded, in the consumed sweep.
	CreatedAt time.Time
	// VolumeBackupID is the session's volume backup as the capture left it;
	// HasVolume marks a session with a volume, whose capture is remote only
	// with that backup.
	VolumeBackupID string
	HasVolume      bool
}

// SandboxLease is what a renewal reports: the fields apid may change while
// agentd holds the session.
type SandboxLease struct {
	CloseRequested bool
	// PauseRequested: apid asked to pause a persistent session.
	PauseRequested bool
	// ForkRequested: apid queued forks of the running session and waits for
	// its fork capture (ADR-20261122132055 §3).
	ForkRequested bool
	IdleTimeout   time.Duration
	ExpiresAt     time.Time
}

// ClaimSandbox moves the oldest claimable queued session to starting under
// token. A session is claimable when its account has fewer live runs and
// sessions than maxLive returns for its plan.
func (s *Store) ClaimSandbox(ctx context.Context, owner, token string, ttl time.Duration, maxLive func(plan string) int) (claim SandboxClaim, found bool, err error) {
	var id string
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		candidates, err := s.q.ListClaimableSandboxSessions(ctx, tx, claimWindow)
		if err != nil {
			return fmt.Errorf("list claimable sessions: %w", err)
		}
		for _, c := range candidates {
			ok, err := s.claimable(ctx, tx, c.AccountID, maxLive)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			n, err := s.q.StartSandboxSession(ctx, tx, sqlc.StartSandboxSessionParams{LeaseToken: token, LeaseOwner: owner,
				TtlSeconds: ttl.Seconds(), SessionID: c.ID})
			if err != nil {
				return fmt.Errorf("start session: %w", err)
			}
			if n == 1 {
				id = c.ID
				return nil
			}
		}
		return nil
	})
	if err != nil || id == "" {
		return SandboxClaim{}, false, err
	}
	row, err := s.q.GetSandboxSessionClaim(ctx, s.pool, id)
	if err != nil {
		return SandboxClaim{}, false, fmt.Errorf("read claimed session: %w", err)
	}
	var task *SandboxTask
	if row.TaskKind != "" {
		task = &SandboxTask{Kind: row.TaskKind, URL: row.TaskUrl, MaxChars: int(row.TaskMaxChars)}
	}
	var resume *SandboxCapture
	if row.State == SandboxResuming && row.CaptureID != "" {
		resume = &SandboxCapture{ID: row.CaptureID, NodeID: row.CaptureNodeID, StorageKey: row.CaptureStorageKey,
			VMStateStorageKey: row.CaptureVmstateStorageKey, FCVersion: row.CaptureFcVersion, MemBytes: row.CaptureMemBytes,
			Remote: row.Durability == "remote"}
	}
	volumeBackup, forkOf := row.VolumeBackupID, ""
	if row.ForkCaptureID != "" {
		// Before the upload only the capture's node holds it (same-node
		// forks, ADR-20261122132055 §3).
		resume = &SandboxCapture{ID: row.CaptureID, NodeID: row.CaptureNodeID, StorageKey: row.CaptureStorageKey,
			VMStateStorageKey: row.CaptureVmstateStorageKey, FCVersion: row.CaptureFcVersion, MemBytes: row.CaptureMemBytes,
			Remote: row.CaptureUploaded}
		volumeBackup, forkOf = row.CaptureVolumeBackupID, row.CaptureSessionID
	}
	return SandboxClaim{ForkOf: forkOf, Kind: row.Kind, VCPU: int(row.Vcpu), Resuming: row.State == SandboxResuming, Resume: resume,
		VolumeGB: int(row.VolumeGb), VolumeBackupID: volumeBackup, VolumeNodeID: row.VolumeNodeID,
		VolumeStoredBackupID: row.VolumeStoredBackupID, ID: row.ID, AccountID: row.AccountID, Plan: row.Plan, RAMMB: int(row.RamMb), Network: row.NetworkMode,
		WorkspaceKey: row.WorkspaceKey, WorkspaceMissing: row.WorkspaceMissing, ImageKey: row.ImageKey,
		ImageMissing: row.FromImage && row.ImageKey == "", StartCmd: row.StartCmd, ReadyCmd: row.ReadyCmd,
		ReadyTimeout: time.Duration(row.ReadyTimeoutS) * time.Second, Browser: row.Browser, Task: task, EgressAllow: row.EgressAllow, EgressDeny: row.EgressDeny, LeaseToken: token,
		SandboxLease: SandboxLease{CloseRequested: row.CloseRequested, IdleTimeout: time.Duration(row.IdleTimeoutSec) * time.Second,
			ExpiresAt: row.ExpiresAt.Time.UTC()}}, true, nil
}

// MarkSandboxPlaced records the node schedd placed the session on, before
// it boots (ADR-20261117190000).
func (s *Store) MarkSandboxPlaced(ctx context.Context, id, token, nodeID string) error {
	n, err := s.q.MarkSandboxSessionPlaced(ctx, s.pool, sqlc.MarkSandboxSessionPlacedParams{NodeID: nodeID, SessionID: id, LeaseToken: token})
	return fenced(n, err, "mark session placed")
}

// MarkSandboxReady records where the session's VM runs and moves it to
// ready.
func (s *Store) MarkSandboxReady(ctx context.Context, id, token, nodeID, instanceID string) error {
	n, err := s.q.MarkSandboxSessionReady(ctx, s.pool, sqlc.MarkSandboxSessionReadyParams{NodeID: nodeID, InstanceID: instanceID,
		SessionID: id, LeaseToken: token})
	return fenced(n, err, "mark session ready")
}

// RenewSandbox extends the lease and records the last call agentd has seen.
// ErrLeaseLost: the session is no longer this caller's.
func (s *Store) RenewSandbox(ctx context.Context, id, token string, ttl time.Duration, lastActivity time.Time) (SandboxLease, error) {
	row, err := s.q.RenewSandboxSessionLease(ctx, s.pool, sqlc.RenewSandboxSessionLeaseParams{TtlSeconds: ttl.Seconds(),
		LastActivityAt: pgtype.Timestamptz{Time: lastActivity, Valid: true}, SessionID: id, LeaseToken: token})
	if errors.Is(err, pgx.ErrNoRows) {
		return SandboxLease{}, ErrLeaseLost
	}
	if err != nil {
		return SandboxLease{}, fmt.Errorf("renew session lease: %w", err)
	}
	return SandboxLease{CloseRequested: row.CloseRequested, PauseRequested: row.PauseRequested, ForkRequested: row.ForkRequested,
		IdleTimeout: time.Duration(row.IdleTimeoutSec) * time.Second, ExpiresAt: row.ExpiresAt.Time.UTC()}, nil
}

// MarkSandboxResumed moves a resumed session to ready on its new VM
// (ADR-20261122132055 §2), uses up its capture, and sets its next automatic
// pause run from now. cold reports a resume that could not restore the
// capture and cold-booted.
func (s *Store) MarkSandboxResumed(ctx context.Context, id, token, nodeID, instanceID string, cold bool, run time.Duration) error {
	n, err := s.q.MarkSandboxSessionResumed(ctx, s.pool, sqlc.MarkSandboxSessionResumedParams{SessionID: id, LeaseToken: token,
		NodeID: nodeID, InstanceID: instanceID, ResumedCold: cold, RunSeconds: run.Seconds()})
	return fenced(n, err, "mark session resumed")
}

// BeginSandboxPause moves a running persistent session to pausing.
func (s *Store) BeginSandboxPause(ctx context.Context, id, token string) error {
	n, err := s.q.BeginSandboxSessionPause(ctx, s.pool, sqlc.BeginSandboxSessionPauseParams{SessionID: id, LeaseToken: token})
	return fenced(n, err, "begin session pause")
}

// CancelSandboxPause returns a pausing session to ready, or frozen: vmmd
// refused the pause and the VM runs on.
func (s *Store) CancelSandboxPause(ctx context.Context, id, token string, frozen bool) error {
	n, err := s.q.CancelSandboxSessionPause(ctx, s.pool, sqlc.CancelSandboxSessionPauseParams{Frozen: frozen, SessionID: id, LeaseToken: token})
	return fenced(n, err, "cancel session pause")
}

// ReturnSandboxResume puts a resume that never booted back to paused, its
// capture kept. retry leaves the resume asked for.
func (s *Store) ReturnSandboxResume(ctx context.Context, id, token string, retry bool, after time.Duration) error {
	n, err := s.q.ReturnSandboxSessionResume(ctx, s.pool, sqlc.ReturnSandboxSessionResumeParams{Retry: retry,
		RetrySeconds: after.Seconds(), SessionID: id, LeaseToken: token})
	return fenced(n, err, "return session resume")
}

// TouchSandboxCaptures records that a sweep found these captures not yet
// uploaded, so the next sweeps look at others first.
func (s *Store) TouchSandboxCaptures(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	uuids := make([]pgtype.UUID, 0, len(ids))
	for _, id := range ids {
		var u pgtype.UUID
		if err := u.Scan(id); err != nil {
			return fmt.Errorf("touch session captures: %w", err)
		}
		uuids = append(uuids, u)
	}
	if err := s.q.TouchSandboxSessionCaptures(ctx, s.pool, uuids); err != nil {
		return fmt.Errorf("touch session captures: %w", err)
	}
	return nil
}

// RecordAbandonedSandboxCapture records a pause agentd never saw answered
// as a used capture, so the sweep deletes whatever vmmd wrote under its key.
func (s *Store) RecordAbandonedSandboxCapture(ctx context.Context, sessionID string, c SandboxCapture) error {
	if err := s.q.RecordAbandonedSandboxSessionCapture(ctx, s.pool, sqlc.RecordAbandonedSandboxSessionCaptureParams{
		CaptureID: c.ID, SessionID: sessionID, NodeID: c.NodeID, StorageKey: c.StorageKey, VmstateStorageKey: c.VMStateStorageKey}); err != nil {
		return fmt.Errorf("record abandoned session capture: %w", err)
	}
	return nil
}

// MarkSandboxPaused records a pausing session's capture and moves it to
// paused: it holds no lease and no VM, and expires retention from now unless
// it resumes first.
func (s *Store) MarkSandboxPaused(ctx context.Context, id, token string, c SandboxCapture, retention time.Duration) error {
	n, err := s.q.MarkSandboxSessionPaused(ctx, s.pool, sqlc.MarkSandboxSessionPausedParams{SessionID: id, LeaseToken: token,
		CaptureID: c.ID, NodeID: c.NodeID, StorageKey: c.StorageKey, VmstateStorageKey: c.VMStateStorageKey, FcVersion: c.FCVersion,
		MemBytes: c.MemBytes, VmstateBytes: c.VMStateBytes, HasBacking: c.HasBacking, VolumeBackupID: c.VolumeBackupID,
		RetentionSeconds: retention.Seconds()})
	return fenced(n, err, "mark session paused")
}

// ConsumedSandboxCaptures lists at most limit captures whose objects the
// sweep deletes: used by a resume, or left by a session that ended paused.
func (s *Store) ConsumedSandboxCaptures(ctx context.Context, limit int) ([]SandboxCapture, error) {
	rows, err := s.q.ListConsumedSandboxSessionCaptures(ctx, s.pool, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("list consumed session captures: %w", err)
	}
	out := make([]SandboxCapture, 0, len(rows))
	for _, r := range rows {
		out = append(out, SandboxCapture{ID: r.ID, StorageKey: r.StorageKey, VMStateStorageKey: r.VmstateStorageKey,
			HasBacking: r.HasBacking, CreatedAt: r.CreatedAt.Time.UTC()})
	}
	return out, nil
}

// NodeOnlySandboxCaptures lists at most limit live captures of paused
// sessions whose durability is still node.
func (s *Store) NodeOnlySandboxCaptures(ctx context.Context, limit int) ([]SandboxCapture, error) {
	rows, err := s.q.ListNodeOnlySandboxCaptures(ctx, s.pool, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("list node-only session captures: %w", err)
	}
	out := make([]SandboxCapture, 0, len(rows))
	for _, r := range rows {
		out = append(out, SandboxCapture{ID: r.ID, SessionID: r.SessionID, AccountID: r.AccountID, StorageKey: r.StorageKey,
			VMStateStorageKey: r.VmstateStorageKey, HasBacking: r.HasBacking, VolumeBackupID: r.VolumeBackupID, HasVolume: r.HasVolume})
	}
	return out, nil
}

// MarkSandboxCaptureRemote records that the shared store holds a paused
// session's whole capture: its durability is remote, and any node resumes
// it. A session that resumed or ended meanwhile is left alone.
func (s *Store) MarkSandboxCaptureRemote(ctx context.Context, sessionID, captureID string) error {
	if _, err := s.q.MarkSandboxCaptureRemote(ctx, s.pool, sqlc.MarkSandboxCaptureRemoteParams{SessionID: sessionID, CaptureID: captureID}); err != nil {
		return fmt.Errorf("mark session capture remote: %w", err)
	}
	return nil
}

// DeleteSandboxCapture removes a consumed capture's row once its objects are
// gone.
func (s *Store) DeleteSandboxCapture(ctx context.Context, id string) error {
	if _, err := s.q.DeleteSandboxSessionCapture(ctx, s.pool, id); err != nil {
		return fmt.Errorf("delete session capture: %w", err)
	}
	return nil
}

// SetSandboxFrozen moves a live session between ready and frozen.
func (s *Store) SetSandboxFrozen(ctx context.Context, id, token string, frozen bool) error {
	n, err := s.q.SetSandboxSessionFrozen(ctx, s.pool, sqlc.SetSandboxSessionFrozenParams{Frozen: frozen, SessionID: id, LeaseToken: token})
	return fenced(n, err, "set session frozen")
}

// RequeueSandbox returns a starting session, which has no VM, to the queue.
func (s *Store) RequeueSandbox(ctx context.Context, id, token string) error {
	n, err := s.q.RequeueSandboxSession(ctx, s.pool, sqlc.RequeueSandboxSessionParams{SessionID: id, LeaseToken: token})
	return fenced(n, err, "requeue session")
}

// PauseLostSandbox returns a held persistent session whose VM ended
// unexpectedly to paused without a capture, kept for retention: its volume
// holds its files, so it resumes cold (ADR-20261122132055 §4). It reports
// false for a session with no volume, which FinishSandbox ends instead.
func (s *Store) PauseLostSandbox(ctx context.Context, id, token string, retention time.Duration) (bool, error) {
	n, err := s.q.PauseLostSandboxSession(ctx, s.pool, sqlc.PauseLostSandboxSessionParams{RetentionSeconds: retention.Seconds(),
		SessionID: id, LeaseToken: token})
	if err != nil {
		return false, fmt.Errorf("pause lost session: %w", err)
	}
	return n == 1, nil
}

// RequeueSandboxForkElsewhere puts a fork its capture's node refused back in
// the queue to start on any node once the shared store holds the capture.
func (s *Store) RequeueSandboxForkElsewhere(ctx context.Context, id, token string) error {
	n, err := s.q.RequeueSandboxForkElsewhere(ctx, s.pool, sqlc.RequeueSandboxForkElsewhereParams{SessionID: id, LeaseToken: token})
	return fenced(n, err, "requeue fork elsewhere")
}

// FinishSandbox ends a held session in state, one of SandboxClosed,
// SandboxExpired and SandboxFailed, with its outcome.
func (s *Store) FinishSandbox(ctx context.Context, id, token, state, code, message string) error {
	if !SandboxTerminal(state) {
		return fmt.Errorf("finish session: %q is not a terminal state", state)
	}
	n, err := s.q.FinishSandboxSession(ctx, s.pool, sqlc.FinishSandboxSessionParams{State: state, OutcomeCode: code,
		OutcomeMessage: optText(message), SessionID: id, LeaseToken: token})
	return fenced(n, err, "finish session")
}

// ExpireSandboxLeases queues expired starting sessions again, pauses
// expired persistent ones with a volume and fails the other expired live
// ones, whose VM ended with their replica's stream, and expires queued
// sessions past their end.
func (s *Store) ExpireSandboxLeases(ctx context.Context) (requeued, failed, expired int64, err error) {
	if requeued, err = s.q.RequeueExpiredStartingSandboxSessions(ctx, s.pool); err != nil {
		return 0, 0, 0, fmt.Errorf("requeue expired sessions: %w", err)
	}
	// A lost resume goes back to paused and resumes on the next claim
	// (ADR-20261122132055 §2).
	returned, err := s.q.ReturnExpiredResumingSandboxSessions(ctx, s.pool)
	if err != nil {
		return requeued, 0, 0, fmt.Errorf("return expired resumes: %w", err)
	}
	requeued += returned
	// A persistent session with a volume lost its VM, not its files: it
	// goes back to paused and resumes cold (ADR-20261122132055 §4).
	plans := make([]string, 0, len(api.SandboxPausedRetentionDays))
	days := make([]int32, 0, len(api.SandboxPausedRetentionDays))
	for _, p := range []api.Plan{api.PlanFree, api.PlanHobby, api.PlanPro, api.PlanScale} {
		plans = append(plans, string(p))
		days = append(days, int32(api.SandboxPausedRetentionDays[p.PlanIndex()])) //nolint:gosec // G115: days of retention.
	}
	paused, err := s.q.PauseExpiredPersistentSandboxSessions(ctx, s.pool, sqlc.PauseExpiredPersistentSandboxSessionsParams{
		RetentionDays: days, Plans: plans})
	if err != nil {
		return requeued, 0, 0, fmt.Errorf("pause expired persistent sessions: %w", err)
	}
	requeued += paused
	if failed, err = s.q.FailExpiredLiveSandboxSessions(ctx, s.pool); err != nil {
		return requeued, 0, 0, fmt.Errorf("fail expired sessions: %w", err)
	}
	if expired, err = s.q.ExpireQueuedSandboxSessions(ctx, s.pool); err != nil {
		return requeued, failed, 0, fmt.Errorf("expire queued sessions: %w", err)
	}
	// A paused session ends without a VM: closed when asked, expired past its
	// retention; its capture is left for the capture sweep.
	ended, err := s.q.EndPausedSandboxSessions(ctx, s.pool)
	if err != nil {
		return requeued, failed, expired, fmt.Errorf("end paused sessions: %w", err)
	}
	expired += int64(len(ended))
	// A queued session whose seller image cannot be had fails instead of
	// waiting out its lifetime.
	unavailable, err := s.q.FailImageUnavailableSandboxSessions(ctx, s.pool)
	if err != nil {
		return requeued, failed, expired, fmt.Errorf("fail sessions without their image: %w", err)
	}
	return requeued, failed + unavailable, expired, nil
}
