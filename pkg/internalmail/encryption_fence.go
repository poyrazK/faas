package internalmail

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
)

// Acquire before other encryption writer locks; admission takes recovery first. The caller retains this transaction
// fence through commit, even with rotation management disabled.
func (s *PGStore) shareEncryption(ctx context.Context, tx pgx.Tx) error {
	return s.queries.ShareEncryption(ctx, tx, s.namespace)
}

func (s *PGStore) checkEncryptionWrite(ctx context.Context, tx pgx.Tx, sealed, fingerprint []byte, fresh bool) error {
	state, err := s.queries.EncryptionState(ctx, tx, s.namespace)
	if errors.Is(err, pgx.ErrNoRows) {
		if len(sealed) > 0 {
			id, parseErr := envelopeKeyID(sealed)
			if parseErr != nil || id != "legacy" {
				return ErrEncryptionRevision
			}
		}
		if len(fingerprint) > 0 {
			id, parseErr := fingerprintKeyID(fingerprint)
			if parseErr != nil || id != "legacy" {
				return ErrEncryptionRevision
			}
		}
		return nil
	}
	if err != nil {
		return err
	}
	var ids []string
	if len(sealed) > 0 {
		id, err := envelopeKeyID(sealed)
		if err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if len(fingerprint) > 0 {
		id, err := fingerprintKeyID(fingerprint)
		if err != nil {
			return err
		}
		ids = append(ids, id)
	}
	for _, id := range ids {
		// Fresh writes must carry explicit versions, even when the selected
		// identity is the legacy identity. Copying a retained signer may use
		// an older nonretired key, under the same shared fence.
		if fresh && id != state.ActiveKeyID {
			return ErrEncryptionRevision
		}
		if id == "legacy" {
			id = state.LegacyKeyID
		}
		retired, err := s.queries.EncryptionKeyRetired(ctx, tx, q.EncryptionKeyRetiredParams{Namespace: s.namespace, KeyID: id})
		if err != nil {
			return err
		}
		if retired {
			return ErrEncryptionKeyRequired
		}
	}
	return nil
}
