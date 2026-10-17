package objectnode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectmultipart"
	"github.com/onebox-faas/faas/pkg/objectrecovery"
)

type RecoveryCollection struct {
	Examined        int       `json:"examined"`
	ReceiptsRemoved int       `json:"receipts_removed"`
	UploadsRemoved  int       `json:"uploads_removed"`
	Protected       int       `json:"protected"`
	NextCursor      string    `json:"next_cursor"`
	Done            bool      `json:"done"`
	RetiredBefore   time.Time `json:"retired_before"`
}

// CollectRecovery performs a bounded-memory offline maintenance page. Directory
// scans are cancellable; the caller continues with NextCursor until Done. Every
// retirement is a single journal decision covering receipt and session together.
func (s *Store) CollectRecovery(ctx context.Context, after string, limit int) (RecoveryCollection, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	var result RecoveryCollection
	if s.closed {
		return result, ErrClosed
	}
	if limit < 1 || limit > api.ObjectNodeRecoveryMaxBatch || after != "" && !validReceiptID(after) {
		return result, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := s.finishPending(false); err != nil {
		return result, err
	}
	if s.replication == nil {
		return result, ErrReplicationOff
	}
	r, err := s.readRecoveryRetention()
	if err != nil {
		return result, err
	}
	ids, more, err := s.recoveryCandidates(ctx, after, limit)
	if err != nil {
		return result, err
	}
	floor := maxRecoveryTime(r.RetiredBefore, s.recoveryTime().Add(-api.ObjectNodeRecoveryWindow).Truncate(time.Millisecond))
	if floor.After(r.RetiredBefore) {
		r.RetiredBefore, r.Retirement = floor, nil
		if _, err = s.writeRecord(filepath.Join(s.root, "recovery.json"), sealRecovery(r)); err != nil {
			return result, err
		}
	}
	r.Retirement = nil
	result.RetiredBefore = r.RetiredBefore
	for _, id := range ids {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		target, e := s.recoveryTarget(id, r.RetiredBefore)
		if e != nil {
			return result, e
		}
		if target != nil {
			r.Retirement = target
			if _, err = s.writeRecord(filepath.Join(s.root, "recovery.json"), sealRecovery(r)); err != nil {
				return result, err
			}
			if target.ReceiptSeal != "" {
				result.ReceiptsRemoved++
			}
			if target.SessionSeal != "" {
				result.UploadsRemoved++
			}
		} else {
			result.Protected++
		}
		result.Examined++
		result.NextCursor = id
	}
	result.Done = !more
	if result.Done {
		result.NextCursor = ""
	}
	return result, nil
}

func (s *Store) recoveryCandidates(ctx context.Context, after string, limit int) ([]string, bool, error) {
	ids := make([]string, 0, limit+1)
	add := func(id string) {
		if id <= after {
			return
		}
		index, found := slices.BinarySearch(ids, id)
		if !found && index <= limit {
			ids = slices.Insert(ids, index, id)
			if len(ids) > limit+1 {
				ids = ids[:limit+1]
			}
		}
	}
	if err := visitDirectory(filepath.Join(s.root, "receipts"), func(e os.DirEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		if !validReceiptID(id) || e.Name() != id+".json" || !e.Type().IsRegular() {
			return ErrCorrupt
		}
		add(id)
		return nil
	}); err != nil {
		return nil, false, err
	}
	if err := visitDirectory(filepath.Join(s.root, "multipart"), func(e os.DirEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !e.IsDir() || !validReceiptID(e.Name()) {
			return ErrCorrupt
		}
		u, err := s.readMultipart(e.Name())
		if err == nil {
			add(u.ReceiptID)
		}
		return err
	}); err != nil {
		return nil, false, err
	}
	more := len(ids) > limit
	if more {
		ids = ids[:limit]
	}
	return ids, more, nil
}

func (s *Store) recoveryTarget(id string, cutoff time.Time) (*recoveryRetirement, error) {
	issued, bounded := objectrecovery.IssuedAt(id)
	if !bounded || issued.After(cutoff) {
		return nil, nil
	}
	r, err := s.readReceipt(id)
	if errors.Is(err, ErrNoReceipt) {
		return s.abortedRecoveryTarget(id, cutoff)
	}
	if err != nil {
		return nil, err
	}
	if r.Status == receiptCommitting || r.SettledAt == nil || r.SettledAt.After(cutoff) {
		return nil, nil
	}
	t := &recoveryRetirement{ID: id, Bucket: r.Bucket, Key: r.Object.Key, ObjectID: r.Object.ID, ReceiptSeal: r.Seal}
	if live, err := s.recoveryObjectLive(*t); err != nil || live {
		return nil, err
	}
	if r.Kind == "multipart" {
		u, err := s.readMultipart(objectmultipart.NativeUploadID(id))
		if err != nil || !matchingTerminalMultipart(r, u) {
			return nil, ErrCorrupt
		}
		if u.SettledAt == nil || u.SettledAt.After(cutoff) {
			return nil, nil
		}
		t.SessionSeal = u.Seal
	}
	return t, nil
}

func (s *Store) abortedRecoveryTarget(id string, cutoff time.Time) (*recoveryRetirement, error) {
	u, err := s.readMultipart(objectmultipart.NativeUploadID(id))
	if err != nil {
		return nil, err
	}
	if u.Status != multipartAborted && u.Status != multipartExpired || u.SettledAt == nil || u.SettledAt.After(cutoff) || u.ExpiresAt.After(cutoff) {
		return nil, nil
	}
	return &recoveryRetirement{ID: id, Bucket: u.Bucket, Key: u.Key, SessionSeal: u.Seal}, nil
}

func (s *Store) recoveryObjectLive(t recoveryRetirement) (bool, error) {
	o, err := readObject(s.objectPath(t.Bucket, t.Key))
	if errors.Is(err, ErrNoObject) {
		return false, nil
	}
	return o.ID == t.ObjectID, err
}
