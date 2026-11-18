package state

import "fmt"

// MaxSnapshotSealedKeyBytes bounds a sealed snapshot capture key, as the
// snapshots_sealed_key_check constraint does.
const MaxSnapshotSealedKeyBytes = 4096

// validSnapshotSeal checks a snapshot's seal (ADR-20261110210000 slice 3d-3):
// the key and capture id are both set or both empty, and the id is the
// capture token of the mem storage key, the id restore opens the key by.
func validSnapshotSeal(snap Snapshot) error {
	if len(snap.SealedKey) == 0 && snap.SealedCaptureID == "" {
		return nil
	}
	token, ok := SnapshotCaptureToken(snap.StorageKey)
	if len(snap.SealedKey) == 0 || len(snap.SealedKey) > MaxSnapshotSealedKeyBytes || !ok || token != snap.SealedCaptureID {
		return fmt.Errorf("%w: a sealed snapshot needs its sealed key and its storage key's capture id", ErrInvalidArgument)
	}
	return nil
}
