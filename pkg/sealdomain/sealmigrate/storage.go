package sealmigrate

import (
	"context"
	"errors"
	"fmt"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/state"
)

// s3CredentialNamespace is pkg/s3gateway.CredentialSecretNamespace; this
// package does not import pkg/s3gateway to stay below it.
const s3CredentialNamespace = "object_s3_credential"

// StorageStore is the slice of *state.PgStore the storage re-seal uses.
type StorageStore interface {
	ListObjectS3CredentialsForRekey(ctx context.Context, limit int, afterID string) ([]state.ObjectS3Credential, error)
	ResealStorageCredential(ctx context.Context, id string, old []byte, kid string, sealed []byte) (bool, error)
}

// MigrateStorage re-seals every active Gregale S3 credential, signed-URL
// ones included, to the storage domain recipient alone and files it under
// the domain's key id (ADR 20261022111812, step 3). legacy are the fleet and
// host identities that open credentials sealed before the domain. Revoked
// credentials and expired signed URLs no longer authenticate and are left
// as they are. Run it only after every s3-gatewayd holds the domain
// identity: a re-sealed credential no longer opens with the fleet identity,
// and a gateway without the domain refuses to start over it.
func MigrateStorage(ctx context.Context, store StorageStore, domain *age.X25519Identity, legacy []*age.X25519Identity) (MigrationReport, error) {
	if store == nil || domain == nil || len(legacy) == 0 {
		return MigrationReport{}, errors.New("sealmigrate: storage migration needs the store, the domain identity and the legacy identities")
	}
	kid := domain.Recipient().String()
	report := MigrationReport{Domain: sealdomain.Storage, Target: kid}
	afterID := ""
	for {
		page, err := store.ListObjectS3CredentialsForRekey(ctx, migrateBatch, afterID)
		if err != nil {
			return report, err
		}
		for _, c := range page {
			report.Scanned++
			afterID = c.ID
			sealed, outcome, err := resealBlob(c.SecretSealed, s3CredentialNamespace, 64, domain, legacy)
			if err != nil {
				return report, fmt.Errorf("sealmigrate: reseal storage credential %s: %w", c.ID, err)
			}
			replaced := false
			if outcome == resealWrite {
				if replaced, err = store.ResealStorageCredential(ctx, c.ID, c.SecretSealed, kid, sealed); err != nil {
					return report, err
				}
			}
			report.count(outcome, replaced)
		}
		if len(page) < migrateBatch {
			return report, nil
		}
	}
}
