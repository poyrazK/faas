package fcvm

// ADR-733 sealed crash captures. On a remote storage backend the capture is
// encrypted on the compute node before anything is published: only
// encrypted twins reach the shared store and this node's read-through
// cache. A fork of it is decrypted into the restoring instance's own
// staging. The capture key is sealed to the fleet recipient (the first of
// SetHostIdentities), so any vmmd with the fleet identity can open it.

import (
	"context"
	"errors"
	"fmt"

	"filippo.io/age"

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

// CaptureKeyOpener opens a sealed capture's key somewhere other than this
// node, by reference (ADR-20261110210000 slice 3d: the control plane's
// secretd reads the sealed key from the capture's row). A RuntimeSecretOpener
// that also implements it opens capture keys too.
type CaptureKeyOpener interface {
	OpenCaptureKey(ctx context.Context, appID, captureID string) (*age.X25519Identity, error)
}

// openSealedSnapshot attaches the decrypting backend to a sealed capture.
// With a runtime opener that opens capture keys, the key comes from it and
// this node needs no runtime identity.
func (m *Manager) openSealedSnapshot(ctx context.Context, appID string, snap *Snapshot) error {
	if snap == nil || len(snap.SealedKey) == 0 {
		return nil
	}
	if m.storage == nil {
		return errors.New("fcvm: sealed capture requires a storage backend")
	}
	var identity *age.X25519Identity
	var err error
	if remote, ok := m.runtimeOpener.(CaptureKeyOpener); ok && m.runtimeOpener != nil {
		if appID == "" || snap.CaptureID == "" {
			return errors.New("fcvm: remote capture key needs the app and capture ids")
		}
		identity, err = remote.OpenCaptureKey(ctx, appID, snap.CaptureID)
	} else {
		if len(m.hostIdentities) == 0 {
			return ErrNoCaptureKey
		}
		identity, err = crashcrypt.OpenKey(m.hostIdentities, snap.CaptureID, snap.SealedKey)
	}
	if err != nil {
		return err
	}
	snap.Storage = crashcrypt.Backend{Inner: m.storage, Identity: identity}
	return nil
}
