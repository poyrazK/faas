package agents

// Sandbox forks (ADR-20261122132055 §3). A fork is a queued session
// forked_from its source, with the source's machine, network, image and
// volume size, that restores fork_capture_id: the source's pause capture
// when it was paused, or a fork capture agentd takes of the running source
// when apid asked for one. A fork becomes claimable once the shared store
// holds its whole capture, so any node restores it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/agents/sqlc"
)

// ErrSandboxNotForkable: the source is not running or paused, is a task, is
// closing, or is paused without a capture.
var ErrSandboxNotForkable = errors.New("sandbox session cannot be forked")

// SandboxForkAdmission is a fork request: Count forks of SourceID, of Kind,
// each labelled Labels and expiring at ExpiresAt. QueuedMax and
// PersistentMax are the plan's, checked under the account's agent lock for
// the whole request.
type SandboxForkAdmission struct {
	AccountID     string
	SourceID      string
	Count         int
	Kind          string
	Labels        map[string]string
	ExpiresAt     time.Time
	QueuedMax     int
	PersistentMax int
}

// ForkSandbox queues a's forks and returns them. A paused source's forks
// restore its pause capture; a running source is asked for a fork capture.
func (s *Store) ForkSandbox(ctx context.Context, a SandboxForkAdmission) ([]Sandbox, error) {
	if !isUUID(a.SourceID) {
		return nil, ErrNotFound
	}
	labels, err := json.Marshal(nonNilLabels(a.Labels))
	if err != nil {
		return nil, fmt.Errorf("encode labels: %w", err)
	}
	var ids []string
	err = s.locked(ctx, a.AccountID, func(tx pgx.Tx) error {
		ids, err = s.insertForks(ctx, tx, a, labels)
		return err
	})
	if err != nil {
		return nil, err
	}
	out := make([]Sandbox, 0, len(ids))
	for _, id := range ids {
		sb, err := s.Sandbox(ctx, a.AccountID, id)
		if err != nil {
			return nil, err
		}
		out = append(out, sb)
	}
	return out, nil
}

// insertForks admits a's forks within the account's lock.
func (s *Store) insertForks(ctx context.Context, tx pgx.Tx, a SandboxForkAdmission, labels []byte) ([]string, error) {
	src, err := s.q.GetSandboxForkSource(ctx, tx, sqlc.GetSandboxForkSourceParams{SessionID: a.SourceID, AccountID: a.AccountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read fork source: %w", err)
	}
	if !forkable(src) {
		return nil, ErrSandboxNotForkable
	}
	if err := s.checkForkLimits(ctx, tx, a); err != nil {
		return nil, err
	}
	capture := ""
	if src.State == SandboxPaused {
		capture = src.PauseCaptureID
	} else if _, err := s.q.RequestSandboxSessionFork(ctx, tx, sqlc.RequestSandboxSessionForkParams{SessionID: a.SourceID,
		AccountID: a.AccountID}); err != nil {
		return nil, fmt.Errorf("request fork capture: %w", err)
	} else if err := s.q.NotifySandboxSessionRequest(ctx, tx, a.SourceID); err != nil {
		// Sent at commit; the source's agentd replica asks for the capture
		// at once rather than at its next renewal.
		return nil, fmt.Errorf("notify fork request: %w", err)
	}
	ids := make([]string, 0, a.Count)
	for range a.Count {
		id, err := s.q.InsertSandboxFork(ctx, tx, sqlc.InsertSandboxForkParams{Kind: a.Kind,
			ExpiresAt: pgtype.Timestamptz{Time: a.ExpiresAt, Valid: true}, Labels: labels, ForkCaptureID: capture,
			SourceID: a.SourceID, AccountID: a.AccountID})
		if err != nil {
			return nil, fmt.Errorf("insert fork: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// forkable: a running or paused source with a capture to fork, not a task
// and not closing.
func forkable(src sqlc.GetSandboxForkSourceRow) bool {
	if src.IsTask || src.CloseRequested {
		return false
	}
	switch src.State {
	case SandboxReady, SandboxFrozen:
		return true
	case SandboxPaused:
		return src.PauseCaptureID != ""
	}
	return false
}

// checkForkLimits applies the plan's queued and persistent caps to the
// whole request.
func (s *Store) checkForkLimits(ctx context.Context, tx pgx.Tx, a SandboxForkAdmission) error {
	runs, err := s.q.CountQueuedAgentRuns(ctx, tx, a.AccountID)
	if err != nil {
		return fmt.Errorf("count queued runs: %w", err)
	}
	sessions, err := s.q.CountQueuedSandboxSessions(ctx, tx, a.AccountID)
	if err != nil {
		return fmt.Errorf("count queued sessions: %w", err)
	}
	if queued := int(runs + sessions); queued+a.Count > a.QueuedMax {
		return &LimitError{Limit: "AgentRunQueuedPerAccount", Max: a.QueuedMax, Observed: queued + a.Count}
	}
	if a.Kind != SandboxPersistent {
		return nil
	}
	persistent, err := s.q.CountPersistentSandboxSessions(ctx, tx, a.AccountID)
	if err != nil {
		return fmt.Errorf("count persistent sessions: %w", err)
	}
	if int(persistent)+a.Count > a.PersistentMax {
		return &LimitError{Limit: "SandboxPersistentMax", Max: a.PersistentMax, Observed: int(persistent) + a.Count}
	}
	return nil
}

// RecordSandboxFork records the fork capture vmmd took of a session agentd
// holds: the forks waiting for one restore it, and the request is answered.
// It returns how many forks it serves.
func (s *Store) RecordSandboxFork(ctx context.Context, id, token string, c SandboxCapture) (int, error) {
	n, err := s.q.RecordSandboxSessionFork(ctx, s.pool, sqlc.RecordSandboxSessionForkParams{SessionID: id, LeaseToken: token,
		CaptureID: c.ID, NodeID: c.NodeID, StorageKey: c.StorageKey, VmstateStorageKey: c.VMStateStorageKey, FcVersion: c.FCVersion,
		MemBytes: c.MemBytes, VmstateBytes: c.VMStateBytes, HasBacking: c.HasBacking, VolumeBackupID: c.VolumeBackupID})
	if err != nil {
		return 0, fmt.Errorf("record session fork: %w", err)
	}
	return int(n), nil
}

// UnuploadedSandboxForkCaptures lists at most limit captures forks wait for
// that the shared store may not hold yet.
func (s *Store) UnuploadedSandboxForkCaptures(ctx context.Context, limit int) ([]SandboxCapture, error) {
	rows, err := s.q.ListUnuploadedSandboxForkCaptures(ctx, s.pool, int32(limit)) //nolint:gosec // G115: a small batch size.
	if err != nil {
		return nil, fmt.Errorf("list unuploaded fork captures: %w", err)
	}
	out := make([]SandboxCapture, 0, len(rows))
	for _, r := range rows {
		out = append(out, SandboxCapture{ID: r.ID, SessionID: r.SessionID, AccountID: r.AccountID, StorageKey: r.StorageKey,
			VMStateStorageKey: r.VmstateStorageKey, HasBacking: r.HasBacking, VolumeBackupID: r.VolumeBackupID,
			HasVolume: r.VolumeBackupID != ""})
	}
	return out, nil
}

// MarkSandboxForkCaptureUploaded records that the shared store holds a
// capture forks wait for: they become claimable.
func (s *Store) MarkSandboxForkCaptureUploaded(ctx context.Context, captureID string) error {
	if _, err := s.q.MarkSandboxForkCaptureUploaded(ctx, s.pool, captureID); err != nil {
		return fmt.Errorf("mark fork capture uploaded: %w", err)
	}
	return nil
}

// SweepSandboxForks consumes fork captures no fork waits for any more (the
// capture sweep then deletes their objects) and fails forks whose source
// ended before their capture was taken.
func (s *Store) SweepSandboxForks(ctx context.Context) (consumed, failed int, err error) {
	c, err := s.q.ConsumeSpentSandboxForkCaptures(ctx, s.pool)
	if err != nil {
		return 0, 0, fmt.Errorf("consume spent fork captures: %w", err)
	}
	f, err := s.q.FailOrphanedSandboxForks(ctx, s.pool)
	if err != nil {
		return int(c), 0, fmt.Errorf("fail orphaned forks: %w", err)
	}
	return int(c), int(f), nil
}
