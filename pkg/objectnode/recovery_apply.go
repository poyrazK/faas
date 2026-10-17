package objectnode

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/objectmultipart"
)

// applyRecovery runs only from ordered journal application. It publishes the
// anti-replay floor before unlinking any proof. A crash between the two unlinks
// replays the same sealed decision before ordinary multipart recovery can run.
func (s *Store) applyRecovery(r RecoveryRetention) error {
	if !validRecovery(r) {
		return ErrCorrupt
	}
	old, err := s.readRecoveryRetention()
	if err == nil && old.RetiredBefore.After(r.RetiredBefore) || err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrCorrupt
	}
	if err = writeRecoveryFormat(s.root); err != nil {
		return err
	}
	if _, err = s.persist(filepath.Join(s.root, "recovery.json"), r); err != nil {
		return err
	}
	if t := r.Retirement; t != nil {
		if err = s.recoveryBoundary("barrier synced"); err != nil {
			return err
		}
		return s.applyRecoveryRetirement(r, *t)
	}
	return nil
}

func (s *Store) applyRecoveryRetirement(state RecoveryRetention, t recoveryRetirement) error {
	if t.ReceiptSeal != "" {
		r, err := s.readReceipt(t.ID)
		if err != nil && !errors.Is(err, ErrNoReceipt) {
			return err
		}
		if err == nil && (r.Seal != t.ReceiptSeal || r.Bucket != t.Bucket || r.Object.Key != t.Key || r.Object.ID != t.ObjectID || r.SettledAt == nil || r.SettledAt.After(state.RetiredBefore) || r.Status == receiptCommitting) {
			return ErrCorrupt
		}
		if live, err := s.recoveryObjectLive(t); err != nil || live {
			return ErrCorrupt
		}
	}
	upload := objectmultipart.NativeUploadID(t.ID)
	if t.SessionSeal != "" {
		u, err := s.readMultipart(upload)
		if err != nil && !errors.Is(err, ErrNoUpload) {
			return err
		}
		if err == nil && (u.Seal != t.SessionSeal || u.ReceiptID != t.ID || u.Bucket != t.Bucket || u.Key != t.Key || u.SettledAt == nil || u.SettledAt.After(state.RetiredBefore) || !terminalMultipart(u)) {
			return ErrCorrupt
		}
	}
	if t.ReceiptSeal != "" {
		if err := os.Remove(s.receiptPath(t.ID)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := syncDirectory(filepath.Join(s.root, "receipts")); err != nil {
			return err
		}
		if err := s.recoveryBoundary("receipt removed"); err != nil {
			return err
		}
	}
	if t.SessionSeal != "" {
		if err := os.RemoveAll(s.multipartPath(upload)); err != nil {
			return err
		}
		if err := syncDirectory(filepath.Join(s.root, "multipart")); err != nil {
			return err
		}
		return s.recoveryBoundary("session removed")
	}
	return nil
}

func (s *Store) recoveryBoundary(phase string) error {
	if s.recoveryCheckpoint != nil {
		return s.recoveryCheckpoint(phase)
	}
	return nil
}
