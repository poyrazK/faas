package sealmigrate

import (
	"context"
	"errors"
	"fmt"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/state"
)

// clusterSigningNamespace is the namespace fleet-seal migrate normalizes the
// cluster signing key to (pkg/fleetseal).
const clusterSigningNamespace = "internal_svc"

// ControlStore is the slice of *state.PgStore the control re-seal uses.
type ControlStore interface {
	LoadClusterSigningKey(ctx context.Context) (state.ClusterSigningKey, error)
	ResealClusterSigningKey(ctx context.Context, keyID string, oldSealed, newSealed []byte) error
}

// MigrateControl re-seals the cluster signing key to the control domain
// recipient alone (ADR 20261022111812, step 3). legacy are the fleet and host
// identities that open it today. Run it only after every schedd, on both
// node roles, holds the domain identity: a schedd that cannot open the key
// cannot mint the internal tokens the rest of the fleet verifies, and
// fleet-seal verify, which node join runs, then needs the control identity
// too.
func MigrateControl(ctx context.Context, store ControlStore, domain *age.X25519Identity, legacy []*age.X25519Identity) (MigrationReport, error) {
	if store == nil || domain == nil || len(legacy) == 0 {
		return MigrationReport{}, errors.New("sealmigrate: control migration needs the store, the domain identity and the legacy identities")
	}
	report := MigrationReport{Domain: sealdomain.Control, Target: domain.Recipient().String()}
	key, err := store.LoadClusterSigningKey(ctx)
	if errors.Is(err, state.ErrNotFound) {
		return report, nil
	}
	if err != nil {
		return report, fmt.Errorf("sealmigrate: load cluster signing key: %w", err)
	}
	report.Scanned++
	sealed, outcome, err := resealBlob(key.SealedBlob, clusterSigningNamespace, 4096, domain, legacy)
	if err != nil {
		return report, fmt.Errorf("sealmigrate: reseal cluster signing key: %w", err)
	}
	replaced := false
	if outcome == resealWrite {
		switch err := store.ResealClusterSigningKey(ctx, key.KeyID, key.SealedBlob, sealed); {
		case err == nil:
			replaced = true
		case !errors.Is(err, state.ErrConflict):
			return report, err
		}
	}
	report.count(outcome, replaced)
	return report, nil
}
