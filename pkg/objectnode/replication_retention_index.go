package objectnode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// Rebuilt under the process lock; never a persisted source of recovery proof.
// Entries are counted at journal publication, including uncertain decisions.
type journalRetentionIndex struct {
	through int64
	valid   bool
	refs    map[string]int64
	pending map[string]string
	pins    map[string]int64
}

func (s *Store) initRetentionIndex() {
	s.retentionIndex = &journalRetentionIndex{through: s.replication.BaseSequence, valid: true, refs: make(map[string]int64), pending: make(map[string]string), pins: make(map[string]int64)}
}

func (s *Store) trackRetentionEntry(e JournalEntry) {
	if s.retentionIndex == nil {
		s.initRetentionIndex()
	}
	i := s.retentionIndex
	if e.Sequence <= i.through {
		return
	}
	if e.Sequence != i.through+1 {
		i.valid = false
	}
	i.through = e.Sequence
	if e.Blob != nil {
		i.refs[e.Blob.SHA256]++
	}
}

func (i *journalRetentionIndex) receipt(r writeReceipt) {
	if old := i.pending[r.ID]; old != "" {
		i.pins[old]--
		if i.pins[old] == 0 {
			delete(i.pins, old)
		}
		delete(i.pending, r.ID)
	}
	if r.Status == receiptCommitting {
		i.pending[r.ID] = r.Object.SHA256
		i.pins[r.Object.SHA256]++
	}
}

func (s *Store) loadRetentionReceiptPins(ctx context.Context) error {
	info, err := os.Lstat(filepath.Join(s.root, "receipts"))
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return ErrCorrupt
	}
	return visitDirectory(filepath.Join(s.root, "receipts"), func(e os.DirEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.HasPrefix(e.Name(), ".tmp-") && validReceiptID(strings.TrimPrefix(e.Name(), ".tmp-")) {
			return nil
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		if !validReceiptID(id) || e.Name() != id+".json" {
			return ErrCorrupt
		}
		if _, _, err := retainedFileProof(s.receiptPath(id)); err != nil {
			return err
		}
		r, err := s.readReceipt(id)
		if err == nil {
			s.retentionIndex.receipt(r)
		}
		return err
	})
}
