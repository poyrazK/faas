package objectnode

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectmultipart"
	"github.com/onebox-faas/faas/pkg/objectrecovery"
)

// All projection operations run under mu (or before Open returns). Failure
// disables cleanup without changing the outcome of a durable catalog mutation.
func (s *Store) startRecoveryIndex() {
	if s.cleanupIndex != nil || s.replication == nil || s.replication.Role != "primary" {
		return
	}
	if _, err := s.readRecoveryRetention(); err != nil {
		return
	}
	s.cleanupStats.Enabled = true
	i, err := openRecoveryIndex(s.root)
	if err != nil {
		s.failRecoveryIndex()
		return
	}
	s.cleanupIndex = i
	if !i.ready || !s.cleanupPositionUsable(i.position) {
		err = s.rebuildRecoveryIndex()
	} else {
		err = s.replayRecoveryIndex()
	}
	if err != nil {
		s.failRecoveryIndex()
		return
	}
	s.cleanupStats.Ready = true
	if s.cacheRecoveryIndex() != nil {
		s.failRecoveryIndex()
	}
}

func (s *Store) cleanupPositionUsable(p recoveryIndexPosition) bool {
	r := s.replication
	if p.Stream != r.StreamID || p.Sequence < r.BaseSequence || p.Sequence > r.AppliedSequence || r.AppliedSequence-p.Sequence > api.ObjectNodeRecoveryIndexReplayMax {
		return false
	}
	if p.Sequence == r.BaseSequence {
		return p.Hash == r.BaseHash
	}
	if p.Sequence == r.AppliedSequence {
		return p.Hash == r.AppliedHash
	}
	e, err := s.readJournal(p.Sequence)
	return err == nil && e.Hash == p.Hash
}

func (s *Store) rebuildRecoveryIndex() error {
	if err := s.cleanupIndex.db.Close(); err != nil {
		return err
	}
	s.cleanupIndex = nil
	// openRecoveryIndex has already vetted this private, process-locked file.
	if err := os.Remove(filepath.Join(s.root, recoveryIndexName)); err != nil {
		return err
	}
	i, err := openRecoveryIndex(s.root)
	if err != nil {
		return err
	}
	s.cleanupIndex = i
	i.position = recoveryIndexPosition{Version: 1, Stream: s.replication.StreamID, Sequence: s.replication.AppliedSequence, Hash: s.replication.AppliedHash, Building: true}
	if err = i.savePosition(); err != nil {
		return err
	}
	batch := make([]recoveryIndexCandidate, 0, api.ObjectNodeRecoveryIndexBuildBatch)
	add := func(id string) error {
		due, ok, err := s.recoveryIndexCandidate(id)
		if err != nil || !ok {
			return err
		}
		batch = append(batch, recoveryIndexCandidate{id, due})
		if len(batch) < cap(batch) {
			return nil
		}
		err = i.addBatch(batch)
		batch = batch[:0]
		return err
	}
	err = visitDirectory(filepath.Join(s.root, "receipts"), func(e os.DirEntry) error {
		id := strings.TrimSuffix(e.Name(), ".json")
		if !e.Type().IsRegular() || !validReceiptID(id) || e.Name() != id+".json" {
			return ErrCorrupt
		}
		return add(id)
	})
	if err == nil {
		err = visitDirectory(filepath.Join(s.root, "multipart"), func(e os.DirEntry) error {
			if !e.IsDir() || !validReceiptID(e.Name()) {
				return ErrCorrupt
			}
			u, err := s.readMultipart(e.Name())
			if err != nil {
				return err
			}
			return add(u.ReceiptID)
		})
	}
	if err == nil && len(batch) != 0 {
		err = i.addBatch(batch)
	}
	if err != nil {
		return err
	}
	i.position.Building = false
	if err = i.savePosition(); err != nil {
		return err
	}
	i.ready = true
	s.cleanupStats.IndexRebuilds++
	return nil
}

func (s *Store) replayRecoveryIndex() error {
	i := s.cleanupIndex
	for sequence := i.position.Sequence + 1; sequence <= s.replication.AppliedSequence; sequence++ {
		e, err := s.readJournal(sequence)
		if err != nil || e.Previous != i.position.Hash {
			return ErrCorrupt
		}
		if err = s.updateRecoveryIndex(e); err != nil {
			return err
		}
		s.cleanupStats.IndexReplayed++
	}
	return i.savePosition()
}

func (s *Store) observeRecoveryJournal(e JournalEntry) {
	if s.cleanupIndex == nil || !s.cleanupStats.Ready {
		return
	}
	if s.updateRecoveryIndex(e) != nil || s.cacheRecoveryIndex() != nil {
		s.failRecoveryIndex()
		return
	}
	if s.cleanupCheckpoint != nil {
		if s.cleanupCheckpoint("index applied") != nil {
			s.failRecoveryIndex()
		}
	}
}

func (s *Store) updateRecoveryIndex(e JournalEntry) error {
	i := s.cleanupIndex
	if s.cleanupIndexFault != nil {
		if err := s.cleanupIndexFault(); err != nil {
			return err
		}
	}
	if e.StreamID != i.position.Stream || e.Sequence != i.position.Sequence+1 && (e.Sequence != i.position.Sequence || e.Hash != i.position.Hash) || e.Sequence == i.position.Sequence+1 && e.Previous != i.position.Hash {
		return ErrCorrupt
	}
	i.position.Sequence, i.position.Hash = e.Sequence, e.Hash
	if e.Path == "recovery.json" {
		var r RecoveryRetention
		if json.Unmarshal(e.Data, &r) != nil || !validRecovery(r) {
			return ErrCorrupt
		}
		if r.Retirement != nil {
			return i.remove(r.Retirement.ID)
		}
		return nil
	}
	p := strings.Split(e.Path, "/")
	var id string
	switch {
	case len(p) == 2 && p[0] == "receipts":
		id = strings.TrimSuffix(p[1], ".json")
	case len(p) == 3 && p[0] == "multipart" && p[2] == "session.json":
		var u MultipartUpload
		if json.Unmarshal(e.Data, &u) != nil {
			return ErrCorrupt
		}
		id = u.ReceiptID
	default:
		return nil
	}
	if _, ok := objectrecovery.IssuedAt(id); !ok {
		return nil
	}
	due, ok, err := s.recoveryIndexCandidate(id)
	if err != nil {
		return err
	}
	if !ok {
		return i.remove(id)
	}
	return i.put(id, due)
}

// Eligibility is only a scheduling hint; live object protection is checked
// again under mu immediately before the journaled retirement decision.
func (s *Store) recoveryIndexCandidate(id string) (time.Time, bool, error) {
	issued, ok := objectrecovery.IssuedAt(id)
	if !ok {
		return time.Time{}, false, nil
	}
	r, err := s.readReceipt(id)
	if errors.Is(err, ErrNoReceipt) {
		u, err := s.readMultipart(objectmultipart.NativeUploadID(id))
		if errors.Is(err, ErrNoUpload) {
			return time.Time{}, false, nil
		}
		if err != nil || u.Status != multipartAborted && u.Status != multipartExpired || u.SettledAt == nil {
			return time.Time{}, false, err
		}
		return cleanupEligibility(maxRecoveryTime(issued, maxRecoveryTime(*u.SettledAt, u.ExpiresAt))), true, nil
	}
	if err != nil || r.Status == receiptCommitting || r.SettledAt == nil {
		return time.Time{}, false, err
	}
	at := maxRecoveryTime(issued, *r.SettledAt)
	if r.Kind == "multipart" {
		u, err := s.readMultipart(objectmultipart.NativeUploadID(id))
		if err != nil || !matchingTerminalMultipart(r, u) {
			return time.Time{}, false, ErrCorrupt
		}
		if u.SettledAt == nil {
			return time.Time{}, false, nil
		}
		at = maxRecoveryTime(at, *u.SettledAt)
	}
	return cleanupEligibility(at), true, nil
}

func cleanupEligibility(at time.Time) time.Time {
	at = at.Add(api.ObjectNodeRecoveryWindow)
	if floor := at.Truncate(time.Millisecond); floor.Before(at) {
		return floor.Add(time.Millisecond)
	}
	return at
}

func (s *Store) cacheRecoveryIndex() error {
	_, next, err := s.cleanupIndex.first()
	if err == nil {
		s.cleanupStats.Candidates = s.cleanupIndex.position.Count
		s.cleanupStats.NextDueAt = next
	}
	return err
}

func (s *Store) failRecoveryIndex() {
	s.cleanupStats.Ready, s.cleanupStats.Deferred = false, true
	if s.cleanupIndex != nil {
		s.cleanupIndex.ready = false
	}
}

func (s *Store) closeRecoveryIndex() error {
	i := s.cleanupIndex
	if i == nil {
		return nil
	}
	var err error
	if i.ready && s.replication != nil && !s.replicationDirty && i.position.Sequence == s.replication.AppliedSequence && i.position.Hash == s.replication.AppliedHash {
		err = i.savePosition()
	}
	s.cleanupIndex = nil
	return errors.Join(err, i.db.Close())
}
