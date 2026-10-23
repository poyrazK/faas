package fcvm

// ADR-733 sealed crash captures. On a remote storage backend the capture is
// encrypted on the compute node before anything is published: only
// encrypted twins reach the shared store and this node's read-through
// cache. A fork of it is decrypted into the restoring instance's own
// staging. The capture key is sealed to the fleet recipient (the first of
// SetHostIdentities), so any vmmd with the fleet identity can open it.

import (
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/crashcrypt"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/storage"
)

// ErrNoCaptureKey means vmmd has no fleet identity to seal or open a crash
// capture with. A sealed capture is refused rather than published or
// restored in plaintext.
var ErrNoCaptureKey = errors.New("fcvm: no fleet identity for crash captures")

// SealCaptureStorage returns a storage backend that publishes captureID's
// objects only as encrypted twins, and the capture key sealed to the fleet
// recipient.
func (m *Manager) SealCaptureStorage(captureID string) (storage.StorageBackend, []byte, error) {
	if m.storage == nil {
		return nil, nil, errors.New("fcvm: sealed capture requires a storage backend")
	}
	recipient, err := secretbox.CurrentRecipient(m.hostIdentities)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrNoCaptureKey, err)
	}
	identity, sealed, err := crashcrypt.NewKey(recipient, captureID)
	if err != nil {
		return nil, nil, err
	}
	return crashcrypt.Backend{Inner: m.storage, Identity: identity}, sealed, nil
}

// openSealedSnapshot attaches the decrypting backend to a sealed capture.
func (m *Manager) openSealedSnapshot(snap *Snapshot) error {
	if snap == nil || len(snap.SealedKey) == 0 {
		return nil
	}
	if m.storage == nil {
		return errors.New("fcvm: sealed capture requires a storage backend")
	}
	if len(m.hostIdentities) == 0 {
		return ErrNoCaptureKey
	}
	identity, err := crashcrypt.OpenKey(m.hostIdentities, snap.CaptureID, snap.SealedKey)
	if err != nil {
		return err
	}
	snap.Storage = crashcrypt.Backend{Inner: m.storage, Identity: identity}
	return nil
}
