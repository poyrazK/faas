package s3gateway

import (
	"context"
	"errors"
	"fmt"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

const credentialRekeyBatchSize = 100

// RekeyCredentials re-seals every active Gregale S3 credential under the
// current host-age identity. It runs before the public listener starts, so an
// operator can safely retire the previous identity after every gateway has
// restarted successfully. Completed rows are crash-safe and skipped on retry.
func RekeyCredentials(ctx context.Context, store state.ObjectS3CredentialRekeyStore, identities []*age.X25519Identity) (int, error) {
	if store == nil {
		return 0, errors.New("s3 gateway rekey: store is required")
	}
	recipient, err := secretbox.CurrentRecipient(identities)
	if err != nil {
		return 0, fmt.Errorf("s3 gateway rekey: current identity: %w", err)
	}
	currentKID := recipient.String()
	afterID, rekeyed := "", 0
	for {
		if err := ctx.Err(); err != nil {
			return rekeyed, err
		}
		rows, err := store.ListObjectS3CredentialsForRekey(ctx, credentialRekeyBatchSize, afterID)
		if err != nil {
			return rekeyed, fmt.Errorf("s3 gateway rekey: list credentials: %w", err)
		}
		if len(rows) == 0 {
			return rekeyed, nil
		}
		for _, row := range rows {
			afterID = row.ID
			if row.KID == currentKID {
				continue
			}
			namespace, plaintext, err := secretbox.OpenBytesMulti(identities, row.SecretSealed)
			if err != nil || namespace != CredentialSecretNamespace || len(plaintext) != 40 {
				return rekeyed, fmt.Errorf("s3 gateway rekey: credential %s cannot be opened", row.ID)
			}
			sealed, err := secretbox.SealBytes(recipient, CredentialSecretNamespace, plaintext, 64)
			if err != nil {
				return rekeyed, fmt.Errorf("s3 gateway rekey: credential %s cannot be sealed", row.ID)
			}
			if err := store.ResealObjectS3Credential(ctx, row.ID, row.KID, currentKID, sealed); err != nil {
				// Another gateway may have won the compare-and-swap, or the
				// customer may have revoked the row during startup. Both leave
				// the stale active row absent and are safe to continue past.
				if errors.Is(err, state.ErrNotFound) {
					continue
				}
				return rekeyed, fmt.Errorf("s3 gateway rekey: persist credential %s: %w", row.ID, err)
			}
			rekeyed++
		}
	}
}
