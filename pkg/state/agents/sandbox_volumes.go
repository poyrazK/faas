package agents

// adr: 20261122132055 §4 — a persistent session's workspace volume: the
// node holding its newest copy, its backups, and their cleanup.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/agents/sqlc"
)

// Volume backup reasons and states.
const (
	SandboxVolumeBackupPeriodic = "periodic"
	SandboxVolumeBackupPause    = "pause"
	SandboxVolumeBackupFork     = "fork"
)

// SandboxVolumeBackup is one backup of a session's volume vmmd reported.
type SandboxVolumeBackup struct {
	ID            string
	NodeID        string
	Reason        string
	SizeBytes     int64
	DataChunks    int
	UploadedBytes int64
	// Uploading: the upload continues after the report (a pause's), so the
	// backup is stored only once the shared store holds its manifest.
	Uploading bool
}

// SandboxVolumeRef names a volume or one of its backups for the sweep.
type SandboxVolumeRef struct {
	SessionID string
	AccountID string
	NodeID    string
	BackupID  string
}

// MarkSandboxVolumeNode records that the session's VM booted on nodeID with
// its volume at sizeGB, and returns the node that held the newest copy
// before ("" when none, or the same node).
func (s *Store) MarkSandboxVolumeNode(ctx context.Context, id, token, nodeID string, sizeGB int) (previous string, err error) {
	prev, err := s.q.MarkSandboxSessionVolumeNode(ctx, s.pool, sqlc.MarkSandboxSessionVolumeNodeParams{SessionID: id, LeaseToken: token,
		NodeID: nodeID, SizeGb: int32(sizeGB)}) //nolint:gosec // G115: bounded by the plan's volume cap.
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrLeaseLost
	}
	if err != nil {
		return "", fmt.Errorf("mark session volume node: %w", err)
	}
	if prev == nodeID {
		return "", nil
	}
	return prev, nil
}

// RecordSandboxVolumeBackup records a backup of a session agentd holds as
// its newest. A replayed report changes nothing; one for a session whose
// lease moved on is dropped (the next backup supersedes it).
func (s *Store) RecordSandboxVolumeBackup(ctx context.Context, id, token string, b SandboxVolumeBackup) (bool, error) {
	state := "stored"
	if b.Uploading {
		state = "uploading"
	}
	n, err := s.q.RecordSandboxVolumeBackup(ctx, s.pool, sqlc.RecordSandboxVolumeBackupParams{SessionID: id, LeaseToken: token,
		BackupID: b.ID, NodeID: b.NodeID, Reason: b.Reason, State: state, SizeBytes: b.SizeBytes,
		DataChunks: int32(b.DataChunks), UploadedBytes: b.UploadedBytes}) //nolint:gosec // G115: a volume's chunk count.
	if err != nil {
		return false, fmt.Errorf("record session volume backup: %w", err)
	}
	return n == 1, nil
}

// MarkSandboxVolumeBackupStored records that the shared store holds a
// pause's volume backup.
func (s *Store) MarkSandboxVolumeBackupStored(ctx context.Context, backupID string) error {
	if _, err := s.q.MarkSandboxVolumeBackupStored(ctx, s.pool, backupID); err != nil {
		return fmt.Errorf("mark session volume backup stored: %w", err)
	}
	return nil
}

// SupersededSandboxVolumeBackups lists at most limit backups older than
// their session's newest that no live capture names; one still uploading
// is listed only once uploadWindow has passed.
func (s *Store) SupersededSandboxVolumeBackups(ctx context.Context, limit int, uploadWindow time.Duration) ([]SandboxVolumeRef, error) {
	rows, err := s.q.ListSupersededSandboxVolumeBackups(ctx, s.pool, sqlc.ListSupersededSandboxVolumeBackupsParams{
		UploadWindowSeconds: uploadWindow.Seconds(), MaxRows: int32(limit)}) //nolint:gosec // G115: a small batch size.
	if err != nil {
		return nil, fmt.Errorf("list superseded volume backups: %w", err)
	}
	out := make([]SandboxVolumeRef, 0, len(rows))
	for _, r := range rows {
		out = append(out, SandboxVolumeRef{SessionID: r.SessionID, AccountID: r.AccountID, BackupID: r.ID})
	}
	return out, nil
}

// EndedSandboxVolumes lists at most limit volumes of sessions that ended.
func (s *Store) EndedSandboxVolumes(ctx context.Context, limit int) ([]SandboxVolumeRef, error) {
	rows, err := s.q.ListEndedSandboxVolumes(ctx, s.pool, int32(limit)) //nolint:gosec // G115: a small batch size.
	if err != nil {
		return nil, fmt.Errorf("list ended volumes: %w", err)
	}
	out := make([]SandboxVolumeRef, 0, len(rows))
	for _, r := range rows {
		out = append(out, SandboxVolumeRef{SessionID: r.SessionID, AccountID: r.AccountID, NodeID: r.NodeID})
	}
	return out, nil
}

// SandboxVolumeBackupIDs lists a session's backups not yet deleted.
func (s *Store) SandboxVolumeBackupIDs(ctx context.Context, sessionID string) ([]string, error) {
	ids, err := s.q.ListSandboxVolumeBackupIDs(ctx, s.pool, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list volume backups: %w", err)
	}
	return ids, nil
}

// MarkSandboxVolumeBackupDeleted records that a backup's manifest is gone.
func (s *Store) MarkSandboxVolumeBackupDeleted(ctx context.Context, backupID string) error {
	if _, err := s.q.MarkSandboxVolumeBackupDeleted(ctx, s.pool, backupID); err != nil {
		return fmt.Errorf("mark volume backup deleted: %w", err)
	}
	return nil
}

// DeleteSandboxVolume removes an ended session's volume row once its
// backups are deleted.
func (s *Store) DeleteSandboxVolume(ctx context.Context, sessionID string) error {
	if _, err := s.q.DeleteSandboxVolume(ctx, s.pool, sessionID); err != nil {
		return fmt.Errorf("delete session volume: %w", err)
	}
	return nil
}

// GrowPausedSandboxVolume sets a paused persistent session's volume to
// volumeGB, which is never smaller than it was; it reports false when the
// session is not paused, not persistent, or the size would shrink. A larger
// size uses the session's capture up, so it resumes cold: the capture's
// memory holds the filesystem at its old size.
func (s *Store) GrowPausedSandboxVolume(ctx context.Context, accountID, id string, volumeGB int) (bool, error) {
	_, err := s.q.GrowPausedSandboxVolume(ctx, s.pool, sqlc.GrowPausedSandboxVolumeParams{SessionID: id, AccountID: accountID,
		VolumeGb: int32(volumeGB)}) //nolint:gosec // G115: bounded by the plan's volume cap.
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("grow session volume: %w", err)
	}
	return true, nil
}
