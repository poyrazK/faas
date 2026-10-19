package objectnode

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func retainedFileProof(path string) (scrubFingerprint, int64, error) {
	f, err := openBackupFile(path)
	if err != nil {
		return scrubFingerprint{}, 0, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return scrubFingerprint{}, 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 {
		return scrubFingerprint{}, 0, ErrCorrupt
	}
	return scrubFileFingerprint(info), info.Size(), nil
}

func (s *Store) compactionEvent(ctx context.Context, event string) error {
	return backupCheckpoint(ctx, s.compactionCheckpoint, event)
}
func (s *Store) syncCompaction(directory string) error {
	if s.compactionSync != nil {
		return s.compactionSync(s.replicationPath(directory))
	}
	return syncDirectory(s.replicationPath(directory))
}

func (s *Store) beginOnlineRecords(ctx context.Context, through int64, limit int) error {
	c := s.compaction
	previous := s.replication.BaseHash
	plan := &onlinePrunePlan{}
	var bytes int64
	for sequence := s.replication.BaseSequence + 1; sequence <= through && len(plan.records) < limit; sequence++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		proof, size, err := retainedFileProof(s.replicationPath("log/" + journalName(sequence)))
		if err != nil {
			return err
		}
		if len(plan.records) > 0 && size > api.ObjectNodeCompactionStepMetadataBytes-bytes {
			break
		}
		e, err := s.readJournal(sequence)
		if err != nil {
			return err
		}
		if e.Previous != previous {
			return ErrCorrupt
		}
		record := onlinePruneRecord{sequence: sequence, size: size, proof: proof}
		if e.Blob != nil {
			record.digest = e.Blob.SHA256
		}
		plan.records = append(plan.records, record)
		bytes += size
		previous = e.Hash
	}
	if len(plan.records) == 0 {
		return ErrCorrupt
	}
	plan.floor = plan.records[len(plan.records)-1].sequence
	state := *s.replication
	state.Version, state.BaseSequence, state.BaseHash = 4, plan.floor, previous
	state.Checkpoint = &JournalCheckpoint{Version: 1, Sequence: plan.floor, Hash: previous, CreatedAt: time.Now().UTC(), Pruning: true}
	if p := state.Durability; p != nil && p.Mode == DurabilityLocal && p.ConfirmedSequence < plan.floor {
		state.Durability = nil
	}
	if err := s.compactionEvent(ctx, "before checkpoint"); err != nil {
		return err
	}
	c.plan = plan
	s.replicationDirty = true
	if err := s.saveReplicationState(state); err != nil {
		return err
	}
	s.replicationDirty = false
	if err := s.compactionEvent(ctx, "after checkpoint"); err != nil {
		return err
	}
	return s.resumeOnlineRecords(ctx, limit)
}

func (s *Store) resumeOnlineRecords(ctx context.Context, limit int) (err error) {
	c, plan := s.compaction, s.compaction.plan
	checkpoint := s.replication.Checkpoint
	if checkpoint == nil || !checkpoint.Pruning || checkpoint.Sequence != plan.floor {
		if s.replication.BaseSequence > plan.floor || len(plan.debt) > 0 {
			return ErrCorrupt
		}
		c.plan = nil // An uncertain save was absent, or its completion was adopted.
		return nil
	}
	s.compactionStats.Phase = "records"
	defer func() { err = errors.Join(err, s.flushRecordDebt()) }()
	for count := 0; plan.position < len(plan.records) && count < limit; count++ {
		if err = ctx.Err(); err != nil {
			return err
		}
		r := plan.records[plan.position]
		path := s.replicationPath("log/" + journalName(r.sequence))
		if err = checkReplayFingerprint(path, r.size, r.proof); err != nil {
			return err
		}
		if err = os.Remove(path); err != nil {
			return err
		}
		plan.position++
		s.compactionStats.StepRecords++
		plan.debt = append(plan.debt, r)
		if err = s.compactionEvent(ctx, "pruned record"); err != nil {
			return err
		}
	}
	if err = s.flushRecordDebt(); err != nil {
		return err
	}
	if plan.position < len(plan.records) {
		return nil
	}
	if err = s.syncCompaction("blobs"); err != nil {
		return err
	}
	if err = s.compactionEvent(ctx, "cleanup synced"); err != nil {
		return err
	}
	state, done := *s.replication, *checkpoint
	done.Pruning = false
	state.Checkpoint = &done
	s.replicationDirty = true
	if err = s.saveReplicationState(state); err != nil {
		return err
	}
	s.replicationDirty = false
	c.plan = nil
	return s.compactionEvent(ctx, "completed")
}

func (s *Store) flushRecordDebt() error {
	plan := s.compaction.plan
	if plan == nil || len(plan.debt) == 0 {
		return nil
	}
	if err := s.syncCompaction("log"); err != nil {
		return err
	}
	counts := make(map[string]int64)
	var bytes int64
	for _, r := range plan.debt {
		bytes += r.size
		if r.digest != "" {
			counts[r.digest]++
		}
	}
	if s.journalUsage.Records < int64(len(plan.debt)) || s.journalUsage.JournalBytes < bytes {
		return ErrCorrupt
	}
	for digest, n := range counts {
		if s.retentionIndex.refs[digest] < n {
			s.retentionIndex.valid = false
			return ErrCorrupt
		}
	}
	for digest, n := range counts {
		s.retentionIndex.refs[digest] -= n
		if s.retentionIndex.refs[digest] == 0 {
			delete(s.retentionIndex.refs, digest)
		}
	}
	n := int64(len(plan.debt))
	s.journalUsage.Records -= n
	s.journalUsage.JournalBytes -= bytes
	s.compactionStats.RecordsPruned += n
	s.compactionStats.BytesUnlinked += bytes
	plan.debt = nil
	return nil
}
