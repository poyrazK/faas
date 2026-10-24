package sealmigrate

import (
	"context"
	"errors"
	"fmt"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/state"
)

// BuildStore is the slice of *state.PgStore the build re-seal uses.
type BuildStore interface {
	ListRegistrySealedCredentials(ctx context.Context, afterKind, afterID string, limit int) ([]state.RegistrySealedCredential, error)
	ResealRegistryCredential(ctx context.Context, c state.RegistrySealedCredential, sealed []byte) (bool, error)
}

// MigrateBuild re-seals every app and job private-registry password to the
// build domain recipient alone (ADR 20261022111812, step 3). legacy are the
// fleet and host identities that open passwords sealed before the domain;
// a password only the domain opens is already current. Run it only after
// every imaged holds the domain identity: a re-sealed password no longer
// opens with the fleet identity.
func MigrateBuild(ctx context.Context, store BuildStore, domain *age.X25519Identity, legacy []*age.X25519Identity) (MigrationReport, error) {
	if store == nil || domain == nil || len(legacy) == 0 {
		return MigrationReport{}, errors.New("sealmigrate: build migration needs the store, the domain identity and the legacy identities")
	}
	report := MigrationReport{Domain: sealdomain.Build, Target: domain.Recipient().String()}
	afterKind, afterID := "", ""
	for {
		page, err := store.ListRegistrySealedCredentials(ctx, afterKind, afterID, migrateBatch)
		if err != nil {
			return report, err
		}
		for _, c := range page {
			report.Scanned++
			afterKind, afterID = c.Kind, c.ID
			sealed, outcome, err := resealBlob(c.Sealed, registryNamespace, api.MaxRegistryPasswordBytes, domain, legacy)
			if err != nil {
				return report, fmt.Errorf("sealmigrate: reseal registry credential %s %s: %w", c.Kind, c.ID, err)
			}
			replaced := false
			if outcome == resealWrite {
				if replaced, err = store.ResealRegistryCredential(ctx, c, sealed); err != nil {
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
