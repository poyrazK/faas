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

// registryLister lists the registry passwords the build walk visits.
type registryLister interface {
	ListRegistrySealedCredentials(ctx context.Context, afterKind, afterID string, limit int) ([]state.RegistrySealedCredential, error)
}

// BuildStore is the slice of *state.PgStore the build re-seal uses.
type BuildStore interface {
	registryLister
	ResealRegistryCredential(ctx context.Context, c state.RegistrySealedCredential, sealed []byte) (bool, error)
}

// BuildLister is the slice of *state.PgStore the build check reads.
type BuildLister interface {
	registryLister
	CountImagedKeyedCrashCaptures(ctx context.Context) (int, error)
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
	return walkBuild(ctx, store, domain, legacy, store.ResealRegistryCredential)
}

// CheckBuild counts, without writing, the registry passwords that legacy
// identities still open (ADR 20261022111812, the contract step's
// precondition). It also counts, as skipped, the unexpired crash captures
// whose at-rest state imaged keeps with the fleet key (ADR-733): imaged may
// drop the fleet and host identities only once both counts are zero.
// Captures are sealed at the source, by vmmd to the runtime domain, so
// these only clear by expiring.
func CheckBuild(ctx context.Context, store BuildLister, domain *age.X25519Identity, legacy []*age.X25519Identity) (MigrationReport, error) {
	if store == nil || domain == nil {
		return MigrationReport{}, errors.New("sealmigrate: build check needs the store and the domain identity")
	}
	report, err := walkBuild(ctx, store, domain, legacy, nil)
	if err != nil {
		return report, err
	}
	captures, err := store.CountImagedKeyedCrashCaptures(ctx)
	if err != nil {
		return report, err
	}
	report.Skipped += captures
	return report, nil
}

// walkBuild visits every registry password. With reseal nil it only counts.
func walkBuild(ctx context.Context, store registryLister, domain *age.X25519Identity, legacy []*age.X25519Identity, reseal func(context.Context, state.RegistrySealedCredential, []byte) (bool, error)) (MigrationReport, error) {
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
			if reseal == nil {
				report.checked(outcome)
				continue
			}
			replaced := false
			if outcome == resealWrite {
				if replaced, err = reseal(ctx, c, sealed); err != nil {
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
