//go:build !no_pg

package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"testing"
	"time"
)

func TestPromotionReleaseConfigurationPG(t *testing.T) {
	testPromotionReleaseConfiguration(t, func(t *testing.T) Store { return NewPgStore(pgtest.OpenMigrated(t)) })
}

func TestPromotionReleaseConfigurationPGLocksWritersAndProtectsCapture(t *testing.T) {
	store := NewPgStore(pgtest.OpenMigrated(t))
	f := newPromotionSnapshotFixture(t, store, false)
	f.create(t)
	f.prepare(t)
	for _, statement := range []string{`UPDATE project_environment_promotion_release_config SET payload=payload WHERE promotion_id=$1`, `DELETE FROM project_environment_promotion_release_config WHERE promotion_id=$1`} {
		if _, err := store.pool.Exec(t.Context(), statement, f.promotion.ID); err == nil {
			t.Fatal("retained promotion capture was mutable")
		}
	}
	tx, err := store.pool.Begin(t.Context())
	requirePromotionSnapshot(t, err)
	defer func() { _ = tx.Rollback(t.Context()) }()
	requirePromotionSnapshot(t, lockProjectForReleaseSetTx(t.Context(), tx, f.account.ID, f.project.ID))
	requirePromotionSnapshot(t, lockPromotionWorkloadEnvironmentsTx(t.Context(), tx, f.promotion.ID))
	requirePromotionSnapshot(t, guardPromotionReleaseConfigurationDB(t.Context(), tx, f.promotion, f.candidates, false, false))
	for _, mutate := range []func(context.Context) error{
		func(ctx context.Context) error {
			return store.UpsertAppEnvInScope(ctx, f.account.ID, f.apps[0].ID, "staging", "MODE", "concurrent-value")
		},
		func(ctx context.Context) error {
			return store.SetDeploymentRootfs(ctx, f.sources[0].ID, "/concurrent", "/concurrent", 1)
		},
	} {
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		err := mutate(ctx)
		cancel()
		if err == nil {
			t.Fatal("configuration writer crossed publication locks")
		}
	}
	requirePromotionSnapshot(t, tx.Rollback(t.Context()))
	_, err = store.PublishProjectEnvironmentPromotionReleaseSet(t.Context(), f.account.ID, f.promotion.ID, 1800, f.candidates)
	requirePromotionSnapshot(t, err)
}
