package agentd

// adr: 20261122132055 §4 — a persistent session's workspace volume: agentd
// records the node holding its newest copy and each backup vmmd reports,
// deletes a stale copy on the node the session left, deletes backups a
// newer one superseded, and deletes an ended session's volume and backups.
// Chunks the manifests shared stay in the store (not yet collected).

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/volbackup"
)

// sandboxVolumeSweepBatch bounds one sweep's volume work.
const sandboxVolumeSweepBatch = 50

// markVolumeNode records the node a session with a volume booted on, and
// deletes the stale copy on the node it held before.
func (s *Sandboxes) markVolumeNode(ctx context.Context, ss *session, node string) error {
	// A fork of either kind has its source's volume (ADR-20261122132055 §3).
	if ss.claim.VolumeGB <= 0 {
		return nil
	}
	prev, err := s.store.MarkSandboxVolumeNode(ctx, ss.claim.ID, ss.claim.LeaseToken, node, ss.claim.VolumeGB)
	if err != nil {
		return err
	}
	if prev != "" {
		go s.deleteNodeVolume(context.WithoutCancel(ctx), prev, ss.claim.AccountID, ss.claim.ID)
	}
	return nil
}

// deleteNodeVolume asks schedd to remove a session's volume from a node. A
// copy that stays is stale and never used: vmmd compares backups first.
func (s *Sandboxes) deleteNodeVolume(ctx context.Context, node, accountID, sessionID string) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := s.schedd.DeleteSandboxVolume(ctx, &scheddpb.DeleteSandboxVolumeRequest{NodeId: node, AccountId: accountID,
		SandboxId: sessionID}); err != nil {
		s.log.Warn("agentd: delete stale sandbox volume failed", "sandbox_id", sessionID, "node", node, "err", err)
	}
}

// recordVolumeBackup records a backup of a running session's volume.
func (s *Sandboxes) recordVolumeBackup(ctx context.Context, ss *session, b *scheddpb.SandboxRunVolumeBackup) {
	ss.mu.Lock()
	node := ss.placedNode
	ss.mu.Unlock()
	recorded, err := s.store.RecordSandboxVolumeBackup(ctx, ss.claim.ID, ss.claim.LeaseToken, agentstore.SandboxVolumeBackup{
		ID: b.GetBackupId(), NodeID: node, Reason: agentstore.SandboxVolumeBackupPeriodic, SizeBytes: b.GetSizeBytes(),
		DataChunks: int(b.GetDataChunks()), UploadedBytes: b.GetUploadedBytes()})
	if err != nil {
		s.log.Warn("agentd: record sandbox volume backup", "sandbox_id", ss.claim.ID, "err", err)
		return
	}
	if recorded {
		s.metrics.volumeBackedUp(time.Duration(b.GetBackupMs())*time.Millisecond, b.GetUploadedBytes())
	}
}

// sweepVolumes deletes superseded backups and ended sessions' volumes.
func (s *Sandboxes) sweepVolumes(ctx context.Context) error {
	if s.scfg.Workspaces == nil {
		return nil
	}
	var errs []error
	superseded, err := s.store.SupersededSandboxVolumeBackups(ctx, sandboxVolumeSweepBatch, sandboxCaptureUploadWindow)
	if err != nil {
		return err
	}
	for _, b := range superseded {
		if err := s.deleteVolumeBackup(ctx, b.AccountID, b.SessionID, b.BackupID); err != nil {
			errs = append(errs, err)
		}
	}
	ended, err := s.store.EndedSandboxVolumes(ctx, sandboxVolumeSweepBatch)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, v := range ended {
		if err := s.deleteEndedVolume(ctx, v); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Sandboxes) deleteVolumeBackup(ctx context.Context, accountID, sessionID, backupID string) error {
	key := volbackup.ManifestKey(accountID, sessionID, backupID)
	if err := s.scfg.Workspaces.Delete(ctx, key); err != nil && !errors.Is(err, storage.ErrNotFound) {
		return fmt.Errorf("delete volume backup %s: %w", backupID, err)
	}
	return s.store.MarkSandboxVolumeBackupDeleted(ctx, backupID)
}

// deleteEndedVolume removes an ended session's node copy, then its backups,
// then its row. A node that is gone took its copy with it.
func (s *Sandboxes) deleteEndedVolume(ctx context.Context, v agentstore.SandboxVolumeRef) error {
	if v.NodeID != "" {
		dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err := s.schedd.DeleteSandboxVolume(dctx, &scheddpb.DeleteSandboxVolumeRequest{NodeId: v.NodeID, AccountId: v.AccountID, SandboxId: v.SessionID})
		cancel()
		if err != nil && !nodeGone(err) {
			return fmt.Errorf("delete volume of %s on %s: %w", v.SessionID, v.NodeID, err)
		}
	}
	ids, err := s.store.SandboxVolumeBackupIDs(ctx, v.SessionID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.deleteVolumeBackup(ctx, v.AccountID, v.SessionID, id); err != nil {
			return err
		}
	}
	if err := s.store.DeleteSandboxVolume(ctx, v.SessionID); err != nil {
		return err
	}
	s.log.Info("agentd: sandbox volume deleted", "sandbox_id", v.SessionID, "backups", len(ids))
	return nil
}

// nodeGone reports a deletion that can never succeed and need not: a node
// schedd no longer knows, or a vmmd that keeps no volumes.
func nodeGone(err error) bool {
	switch status.Code(err) {
	case codes.NotFound, codes.Unimplemented:
		return true
	}
	return false
}
