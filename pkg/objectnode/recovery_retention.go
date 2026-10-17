package objectnode

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectrecovery"
)

// RecoveryRetention is a monotone first-use barrier, replicated with the catalog.
// Retirement describes at most one already authorized, restartable collection.
type RecoveryRetention struct {
	Version       int                 `json:"version"`
	WindowSeconds int64               `json:"window_seconds"`
	RetiredBefore time.Time           `json:"retired_before"`
	Retirement    *recoveryRetirement `json:"retirement,omitempty"`
	Seal          string              `json:"seal"`
}

type recoveryRetirement struct {
	ID          string `json:"id"`
	Bucket      string `json:"bucket"`
	Key         string `json:"key"`
	ObjectID    string `json:"object_id"`
	ReceiptSeal string `json:"receipt_seal"`
	SessionSeal string `json:"session_seal"`
}

func sealRecovery(r RecoveryRetention) RecoveryRetention {
	r.Seal = ""
	r.Seal = scrubSeal(r)
	return r
}

func validRecovery(r RecoveryRetention) bool {
	if r.Version != 1 || r.WindowSeconds != int64(api.ObjectNodeRecoveryWindow/time.Second) || r.RetiredBefore.IsZero() || r.RetiredBefore.UnixMilli() < 1 || r.Seal != sealRecovery(r).Seal {
		return false
	}
	if t := r.Retirement; t != nil {
		issued, ok := objectrecovery.IssuedAt(t.ID)
		if !ok || issued.After(r.RetiredBefore) || !ValidBucket(t.Bucket) || !validKey(t.Key) || t.ReceiptSeal == "" && t.SessionSeal == "" {
			return false
		}
		if t.ReceiptSeal != "" && (!validScrubFindingID(t.ReceiptSeal) || !validReceiptID(t.ObjectID)) || t.SessionSeal != "" && !validScrubFindingID(t.SessionSeal) || t.ReceiptSeal == "" && t.ObjectID != "" {
			return false
		}
	}
	return true
}

func (s *Store) recoveryTime() time.Time {
	if s.recoveryClock != nil {
		return s.recoveryClock().UTC()
	}
	return time.Now().UTC()
}

func (s *Store) readRecoveryRetention() (RecoveryRetention, error) {
	var r RecoveryRetention
	err := readJSON(filepath.Join(s.root, "recovery.json"), &r)
	if err != nil {
		return r, err
	}
	if !validRecovery(r) {
		return r, ErrCorrupt
	}
	return r, nil
}

func (s *Store) validateRecoveryCatalog(version int) error {
	_, err := s.readRecoveryRetention()
	if errors.Is(err, os.ErrNotExist) {
		if version == 5 {
			return ErrCorrupt // A serving format-5 node must never forget its barrier.
		}
		return nil
	}
	if err != nil || version != 5 {
		return ErrCorrupt
	}
	return nil
}

func (s *Store) validateRecoveryFormat() error {
	version, err := nodeFormatVersion(s.root)
	if err != nil {
		return err
	}
	return s.validateRecoveryCatalog(version)
}

// A follower can reopen an interrupted first upgrade only to replay its exact
// received recovery decision. Promotion still requires received == applied.
func (s *Store) recoverableMissingBarrier() bool {
	if s.replication == nil || s.replication.ReceivedSequence != s.replication.AppliedSequence+1 {
		return false
	}
	if _, err := s.readRecoveryRetention(); !errors.Is(err, os.ErrNotExist) {
		return false
	}
	e, err := s.readJournal(s.replication.ReceivedSequence)
	return err == nil && e.Path == "recovery.json"
}

// checkNewReceiptID runs only after an exact existing receipt/session lookup.
// Its wall-clock floor also protects restoration of a pre-collection backup.
// The durable floor prevents a clock rollback from reopening collected IDs.
func (s *Store) checkNewReceiptID(id string) error {
	issued, bounded := objectrecovery.IssuedAt(id)
	if !bounded {
		return nil // Legacy identities remain valid and are never collected.
	}
	now := s.recoveryTime()
	floor := now.Add(-api.ObjectNodeRecoveryWindow)
	if r, err := s.readRecoveryRetention(); err == nil {
		floor = maxRecoveryTime(floor, r.RetiredBefore)
	} else {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err = s.validateRecoveryFormat(); err != nil {
			return err
		}
	}
	if !issued.After(floor) {
		return ErrReceiptExpired
	}
	if issued.After(now.Add(api.ObjectNodeRecoveryClockSkew)) {
		return ErrInvalid
	}
	return nil
}

func maxRecoveryTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// EnableRecoveryRetention requires an existing primary journal, even in local
// durability mode. Format 5 makes older executables refuse the directory.
func (s *Store) EnableRecoveryRetention() (RecoveryRetention, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return RecoveryRetention{}, ErrClosed
	}
	if err := s.finishPending(false); err != nil {
		return RecoveryRetention{}, err
	}
	if s.replication == nil {
		return RecoveryRetention{}, ErrReplicationOff
	}
	if r, err := s.readRecoveryRetention(); !errors.Is(err, os.ErrNotExist) {
		return r, err
	}
	r := sealRecovery(RecoveryRetention{Version: 1, WindowSeconds: int64(api.ObjectNodeRecoveryWindow / time.Second), RetiredBefore: s.recoveryTime().Add(-api.ObjectNodeRecoveryWindow).Truncate(time.Millisecond)})
	_, err := s.writeRecord(filepath.Join(s.root, "recovery.json"), r)
	if err == nil {
		s.startRecoveryIndex()
	}
	return r, err
}

func writeRecoveryFormat(root string) error {
	_, err := writeJSON(filepath.Join(root, "format.json"), struct {
		Engine  string `json:"engine"`
		Version int    `json:"version"`
	}{"gregale-object-node", 5})
	return err
}

func (s *Store) RecoveryRetentionStatus() (RecoveryRetention, error) {
	s.life.RLock()
	defer s.life.RUnlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return RecoveryRetention{}, ErrClosed
	}
	return s.readRecoveryRetention()
}
