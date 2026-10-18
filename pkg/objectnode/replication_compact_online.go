package objectnode

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

type CompactionOptions struct {
	TriggerRecords      int64
	TriggerJournalBytes int64
	TriggerBlobBytes    int64
	MinFreeBytes        int64
	MaxRecords          int
	MaxBlobs            int
}

func DefaultCompactionOptions() CompactionOptions {
	return CompactionOptions{TriggerRecords: api.ObjectNodeCompactionTriggerRecords, TriggerJournalBytes: api.ObjectNodeCompactionTriggerJournalBytes, TriggerBlobBytes: api.ObjectNodeCompactionTriggerBlobBytes, MinFreeBytes: api.ObjectNodeCompactionMinFreeBytes, MaxRecords: api.ObjectNodeCompactionStepRecords, MaxBlobs: api.ObjectNodeCompactionStepBlobs}
}

func (o CompactionOptions) Validate() error {
	if o.TriggerRecords < 1 || o.TriggerRecords > api.ObjectNodeReplicationMaxEntries || o.TriggerJournalBytes < 1 || o.TriggerJournalBytes > api.ObjectNodeReplicationJournalBytes || o.TriggerBlobBytes < 1 || o.TriggerBlobBytes > api.ObjectNodeReplicationBlobBytes || o.MinFreeBytes < 0 || o.MaxRecords < 1 || o.MaxRecords > api.ObjectNodeCompactionMaxStepFiles || o.MaxBlobs < 1 || o.MaxBlobs > api.ObjectNodeCompactionMaxStepFiles {
		return ErrInvalid
	}
	return nil
}

// CompactionStatus contains cached process-local observations, not recovery proof.
type CompactionStatus struct {
	Active        bool      `json:"active"`
	Pinned        bool      `json:"pinned"`
	Deferred      bool      `json:"deferred"`
	Phase         string    `json:"phase"`
	Steps         int64     `json:"steps"`
	Failures      int64     `json:"failures"`
	Cycles        int64     `json:"cycles"`
	RecordsPruned int64     `json:"records_pruned"`
	BlobsPruned   int64     `json:"blobs_pruned"`
	BytesUnlinked int64     `json:"bytes_unlinked"`
	StepRecords   int       `json:"step_records"`
	StepBlobs     int       `json:"step_blobs"`
	LastSuccessAt time.Time `json:"last_success_at"`
}

type onlineCompaction struct {
	target   int64
	phase    string
	plan     *onlinePrunePlan
	blobs    *os.File
	entries  []os.DirEntry
	blobDebt JournalUsage
	nextScan time.Time
}

type onlinePrunePlan struct {
	floor    int64
	records  []onlinePruneRecord
	position int
	debt     []onlinePruneRecord
}

type onlinePruneRecord struct {
	sequence, size int64
	digest         string
	proof          scrubFingerprint
}

func (c *onlineCompaction) close() {
	if c.blobs != nil {
		_ = c.blobs.Close()
		c.blobs = nil
	}
	c.entries = nil
}

func (s *Store) CompactionStatus() (CompactionStatus, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return CompactionStatus{}, ErrClosed
	}
	return s.compactionStats, nil
}

// CompactStep never opens a second Store or hashes payloads under the catalog
// lock. Each floor covers a bounded verified journal prefix; live data, receipts
// and multipart records are never deleted. Startup resumes the sealed v4 marker.
func (s *Store) CompactStep(ctx context.Context, o CompactionOptions) (status CompactionStatus, err error) {
	if err = o.Validate(); err != nil {
		return status, err
	}
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := &s.compactionStats
	stats.Steps++
	stats.StepRecords, stats.StepBlobs = 0, 0
	stats.Deferred, stats.Pinned = false, false
	defer func() {
		if err != nil && !errors.Is(err, ErrClosed) && !errors.Is(err, ErrFenced) && !errors.Is(err, ErrReplicationOff) && ctx.Err() == nil {
			stats.Failures++
		}
		status = *stats
	}()
	if s.closed {
		return status, ErrClosed
	}
	if err = ctx.Err(); err != nil {
		return status, err
	}
	if s.replication == nil {
		return status, ErrReplicationOff
	}
	if s.authorityDirty || s.replication.Role == "fenced" {
		stats.Active = false
		stats.Phase = "fenced"
		return status, ErrFenced
	}
	if idle, checkErr := s.compactionCanWait(o); idle || checkErr != nil {
		return status, checkErr
	}
	if s.replication.Role == "primary" {
		err = s.finishReplicationPending()
	} else {
		err = s.adoptReplicationRecord()
	}
	if err != nil {
		return status, err
	}
	if s.replication.ReceivedSequence != s.replication.AppliedSequence {
		stats.Deferred = true
		return status, nil
	}
	if s.retentionIndex == nil || !s.retentionIndex.valid || s.retentionIndex.through != s.replication.ReceivedSequence {
		return status, ErrCorrupt
	}
	if s.compaction == nil {
		var needed bool
		needed, err = s.needsCompaction(o)
		if err != nil || !needed {
			stats.Phase = "idle"
			return status, err
		}
		s.compaction = &onlineCompaction{target: s.replication.AppliedSequence, phase: "records"}
	}
	stats.Active = true
	err = s.compactOnline(ctx, o)
	if err == nil {
		stats.LastSuccessAt = time.Now().UTC()
	}
	return status, err
}

// Cached positions suffice when no deletion is possible. Before actual work,
// reconcile durable state so a lost report acknowledgement cannot be overwritten.
func (s *Store) compactionCanWait(o CompactionOptions) (bool, error) {
	if s.replicationDirty {
		return false, nil
	}
	if s.compaction == nil {
		needed, err := s.needsCompaction(o)
		if !needed {
			s.compactionStats.Phase = "idle"
		}
		return !needed, err
	}
	c := s.compaction
	if c.plan == nil && c.phase == "records" && time.Now().Before(c.nextScan) && s.replication.ReceivedSequence == s.replication.AppliedSequence && retentionSafeSequence(s.replication.ReplicationStatus) == s.replication.BaseSequence {
		s.compactionStats.Active, s.compactionStats.Pinned = true, true
		s.compactionStats.Phase = "pinned"
		return true, nil
	}
	return false, nil
}

func (s *Store) needsCompaction(o CompactionOptions) (bool, error) {
	u := s.journalUsage
	if u.Records == 0 && u.Blobs == 0 {
		return false, nil
	}
	// A projected admission can close below the ordinary maintenance trigger.
	// Reclaim its safe history with the same bounded work and retention pins.
	if a := s.admission; a != nil && a.options.Enabled && a.status.Reasons&(AdmissionRecords|AdmissionJournal|AdmissionReplay|AdmissionFree) != 0 {
		return true, nil
	}
	if u.Records >= o.TriggerRecords || u.Blobs >= o.TriggerRecords || u.JournalBytes >= o.TriggerJournalBytes || u.BlobBytes >= o.TriggerBlobBytes {
		return true, nil
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(s.root, &stat); err != nil {
		return false, err
	}
	available := stat.Bavail * uint64(stat.Bsize)
	return available < uint64(o.MinFreeBytes) || stat.Bavail < stat.Blocks*api.ObjectNodeCompactionFreePercent/100, nil
}

func (s *Store) compactOnline(ctx context.Context, o CompactionOptions) error {
	c := s.compaction
	if err := s.flushBlobDebt(); err != nil {
		return err
	}
	if c.plan != nil {
		return s.resumeOnlineRecords(ctx, o.MaxRecords)
	}
	if s.replication.Checkpoint != nil && s.replication.Checkpoint.Pruning {
		return ErrCorrupt
	}
	safe := retentionSafeSequence(s.replication.ReplicationStatus)
	if s.replication.BaseSequence < c.target && safe > s.replication.BaseSequence {
		c.close()
		c.phase = "records"
		c.nextScan = time.Time{}
		return s.beginOnlineRecords(ctx, min(safe, c.target), o.MaxRecords)
	}
	if c.phase == "records" {
		if s.replication.BaseSequence < c.target && time.Now().Before(c.nextScan) {
			s.compactionStats.Pinned = true
			s.compactionStats.Phase = "pinned"
			return nil
		}
		c.phase = "blobs"
	}
	s.compactionStats.Phase = "blobs"
	return s.scanOnlineBlobs(ctx, o.MaxBlobs)
}

// RunCompactor performs one bounded step per tick, backing off on failures. It
// exits on cancellation, close, fencing or an unreplicated directory.
func (s *Store) RunCompactor(ctx context.Context, o CompactionOptions, observe func(CompactionStatus, error)) {
	if err := o.Validate(); err != nil {
		if observe != nil {
			observe(CompactionStatus{}, err)
		}
		return
	}
	ticker := time.NewTicker(api.ObjectNodeCompactionInterval)
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
		status, err := s.CompactStep(ctx, o)
		if errors.Is(err, ErrClosed) || errors.Is(err, ErrFenced) || errors.Is(err, ErrReplicationOff) {
			return
		}
		if ctx.Err() != nil {
			return
		}
		if observe != nil {
			observe(status, err)
		}
		if err != nil {
			retryAt = time.Now().Add(api.ObjectNodeCompactionRetry)
		}
	}
}

func (f *ReplicaFollower) RunCompactor(ctx context.Context, o CompactionOptions, observe func(CompactionStatus, error)) {
	f.store.RunCompactor(ctx, o, observe)
}
func (f *ReplicaFollower) CompactionStatus() (CompactionStatus, error) {
	return f.store.CompactionStatus()
}
