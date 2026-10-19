package objectnode

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectmultipart"
)

type RecoveryCleanupOptions struct {
	MaxRecords int
	StepTime   time.Duration
}

func DefaultRecoveryCleanupOptions() RecoveryCleanupOptions {
	return RecoveryCleanupOptions{MaxRecords: api.ObjectNodeRecoveryCleanupBatch, StepTime: api.ObjectNodeRecoveryCleanupStepTime}
}

func (o RecoveryCleanupOptions) Validate() error {
	if o.MaxRecords < 1 || o.MaxRecords > api.ObjectNodeRecoveryCleanupMaxBatch || o.StepTime <= 0 || o.StepTime > api.ObjectNodeRecoveryCleanupMaxStepTime {
		return ErrInvalid
	}
	return nil
}

// RecoveryCleanupStatus is cached under mu. Metrics never scan the catalog or
// open the index. Counters are process-local; scheduling checkpoints are durable.
type RecoveryCleanupStatus struct {
	Enabled         bool      `json:"enabled"`
	Ready           bool      `json:"ready"`
	Deferred        bool      `json:"deferred"`
	Candidates      int64     `json:"candidates"`
	NextDueAt       time.Time `json:"next_due_at"`
	Steps           int64     `json:"steps"`
	Failures        int64     `json:"failures"`
	Examined        int64     `json:"examined"`
	Protected       int64     `json:"protected"`
	Stale           int64     `json:"stale"`
	ReceiptsRemoved int64     `json:"receipts_removed"`
	UploadsRemoved  int64     `json:"uploads_removed"`
	IndexRebuilds   int64     `json:"index_rebuilds"`
	IndexReplayed   int64     `json:"index_replayed"`
	StepExamined    int       `json:"step_examined"`
	LastSuccessAt   time.Time `json:"last_success_at"`
}

func (s *Store) RecoveryCleanupStatus() (RecoveryCleanupStatus, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return RecoveryCleanupStatus{}, ErrClosed
	}
	return s.cleanupStats, nil
}

// RecoveryCleanupStep seeks a bounded number of due identities without scanning
// directories. It releases mu between identities. StepTime and cancellation are
// checked between decisions; a started durable retirement must finish its fsyncs.
func (s *Store) RecoveryCleanupStep(ctx context.Context, o RecoveryCleanupOptions) (status RecoveryCleanupStatus, err error) {
	if err = o.Validate(); err != nil {
		return status, err
	}
	if err = ctx.Err(); err != nil {
		return status, err
	}
	s.life.RLock()
	defer s.life.RUnlock()
	if s.closed {
		return status, ErrClosed
	}
	start := time.Now()
	examined := 0
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.cleanupStats.Steps++
		s.cleanupStats.StepExamined = examined
		if err != nil {
			s.cleanupStats.Failures++
			s.cleanupStats.Deferred = true
		} else {
			s.cleanupStats.Deferred = false
			s.cleanupStats.LastSuccessAt = s.recoveryTime()
		}
		status = s.cleanupStats
	}()
	for examined < o.MaxRecords {
		if err = ctx.Err(); err != nil {
			return status, err
		}
		if examined > 0 && time.Since(start) >= o.StepTime {
			break
		}
		var worked bool
		worked, err = s.recoveryCleanupOne(ctx)
		if worked {
			examined++
		}
		if err != nil || !worked {
			return status, err
		}
		if s.cleanupCheckpoint != nil {
			if err = s.cleanupCheckpoint("identity finished"); err != nil {
				return status, err
			}
		}
	}
	return status, nil
}

func (s *Store) recoveryCleanupOne(ctx context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := s.servingError(); err != nil {
		return false, err
	}
	if !s.cleanupStats.Enabled {
		return false, nil
	}
	if !s.cleanupStats.Ready || s.cleanupIndex == nil {
		return false, ErrRecoveryIndex
	}
	id, at, err := s.cleanupIndex.first()
	if err != nil {
		s.failRecoveryIndex()
		return false, ErrRecoveryIndex
	}
	now := s.recoveryTime()
	if id == "" || at.After(now) {
		return false, nil
	}
	if s.replication == nil {
		return false, ErrReplicationOff
	}
	if s.pending != nil || s.replicationDirty || s.replication.ReceivedSequence != s.replication.AppliedSequence {
		return false, ErrRecoveryBusy
	}
	// Reconcile an uncertain source checkpoint without applying a payload.
	// Ordinary recovery owns pending catalog work; this worker only retires
	// metadata after the source is quiescent under the same catalog lock.
	if err = s.adoptReplicationRecord(); err != nil {
		return false, err
	}
	if err = s.servingError(); err != nil {
		return false, err
	}
	if s.replication.ReceivedSequence != s.replication.AppliedSequence {
		return false, ErrRecoveryBusy
	}
	if s.cleanupIndex.position.Sequence != s.replication.AppliedSequence || s.cleanupIndex.position.Hash != s.replication.AppliedHash {
		s.failRecoveryIndex()
		return false, ErrRecoveryIndex
	}
	s.cleanupStats.Examined++
	r, err := s.readRecoveryRetention()
	if err != nil {
		return true, err
	}
	floor := maxRecoveryTime(r.RetiredBefore, now.Add(-api.ObjectNodeRecoveryWindow).Truncate(time.Millisecond))
	target, err := s.recoveryTarget(id, floor)
	if errors.Is(err, ErrNoUpload) {
		// Both exact source records are absent. Prune only the derived hint;
		// this never establishes the outcome of a central write reservation.
		err = s.cleanupIndex.remove(id)
		s.cleanupStats.Stale++
	} else if err == nil {
		if target != nil && target.SessionSeal != "" {
			var empty bool
			empty, err = s.emptyRetirementSession(id)
			if !empty {
				target = nil
			}
		}
		if err == nil && target != nil {
			r.RetiredBefore, r.Retirement = floor, target
			_, err = s.writeRecord(filepath.Join(s.root, "recovery.json"), sealRecovery(r))
			if err == nil {
				if target.ReceiptSeal != "" {
					s.cleanupStats.ReceiptsRemoved++
				}
				if target.SessionSeal != "" {
					s.cleanupStats.UploadsRemoved++
				}
			}
		} else if err == nil {
			err = s.cleanupIndex.deferEntry(id, now.Add(api.ObjectNodeRecoveryCleanupProtected))
			s.cleanupStats.Protected++
		}
	}
	if err != nil {
		s.failRecoveryIndex()
		return true, err
	}
	if !s.cleanupStats.Ready || s.cacheRecoveryIndex() != nil {
		s.failRecoveryIndex()
		return true, ErrRecoveryIndex
	}
	return true, nil
}

// Terminal cleanup should already have removed all part blobs. Never let an
// online retirement turn RemoveAll into unbounded work under the catalog lock.
func (s *Store) emptyRetirementSession(id string) (bool, error) {
	path := filepath.Join(s.root, "multipart", objectmultipart.NativeUploadID(id))
	dir, err := os.Open(path) //nolint:forbidigo // Generated path under the private node catalog.
	if err != nil {
		return false, err
	}
	defer func() { _ = dir.Close() }()
	entries, err := dir.ReadDir(api.ObjectNodeRecoveryTerminalEntries)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	if len(entries) >= api.ObjectNodeRecoveryTerminalEntries {
		return false, ErrCorrupt
	}
	for _, e := range entries {
		if e.Name() == "session.json" && e.Type().IsRegular() {
			continue
		}
		if e.Name() != "parts" || !e.IsDir() {
			return false, ErrCorrupt
		}
		parts, err := os.Open(filepath.Join(path, "parts")) //nolint:forbidigo // Validated private terminal session directory.
		if err != nil {
			return false, err
		}
		part, readErr := parts.ReadDir(1)
		closeErr := parts.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return false, readErr
		}
		if len(part) != 0 || closeErr != nil {
			return false, closeErr
		}
	}
	return true, nil
}

func (s *Store) RunRecoveryCleanup(ctx context.Context, o RecoveryCleanupOptions, observe func(RecoveryCleanupStatus, error)) {
	if err := o.Validate(); err != nil {
		if observe != nil {
			observe(RecoveryCleanupStatus{}, err)
		}
		return
	}
	ticker := time.NewTicker(api.ObjectNodeRecoveryCleanupInterval)
	defer ticker.Stop()
	var retryAt time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if time.Now().Before(retryAt) {
			continue
		}
		status, err := s.RecoveryCleanupStep(ctx, o)
		if ctx.Err() != nil || errors.Is(err, ErrClosed) || errors.Is(err, ErrFenced) || errors.Is(err, ErrReplica) || errors.Is(err, ErrReplicationOff) || err == nil && !status.Enabled {
			return
		}
		if observe != nil {
			observe(status, err)
		}
		if err != nil {
			retryAt = time.Now().Add(api.ObjectNodeRecoveryCleanupRetry)
		}
	}
}
