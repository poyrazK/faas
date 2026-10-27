//go:build !no_pg

package state

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// adr: 20261019120040
func TestPromotionWorkerSetPG(t *testing.T) {
	testPromotionWorkerSet(t, func(t *testing.T) Store { return NewPgStore(pgtest.OpenMigrated(t)) })
	t.Run("recovery", func(t *testing.T) {
		testPromotionRecoveryMode(t, func(t *testing.T) Store { return NewPgStore(pgtest.OpenMigrated(t)) }, true, true)
	})
	t.Run("older_controller_publication", testPromotionWorkerSetPublicationFence)
}

// adr: 20261019120040
func testPromotionWorkerSetPublicationFence(t *testing.T) {
	store := NewPgStore(pgtest.OpenMigrated(t))
	f := newPromotionWorkerSetFixture(t, store, false)
	seedPromotionWorkerSet(t, f, 0, 0)
	promotionWorkerRound(t, f)
	promotionWorkerRound(t, f)
	assertFenced := func(query string) {
		t.Helper()
		_, err := store.pool.Exec(t.Context(), query, f.promotion.ID)
		var constraint *pgconn.PgError
		if !errors.As(err, &constraint) || constraint.ConstraintName != "promotion_worker_set_publication_fenced" {
			t.Fatalf("older controller publication was not fenced: %v", err)
		}
	}
	assertFenced("UPDATE project_environment_promotions SET target_release_set_id=previous_target_release_set_id WHERE id=$1")
	_, err := store.PublishProjectEnvironmentPromotionReleaseSet(t.Context(), f.account.ID, f.promotion.ID, 1800, f.candidates)
	requirePromotionSnapshot(t, err)
	assertFenced("UPDATE project_environment_promotions SET restored_target_release_set_id=previous_target_release_set_id WHERE id=$1")
}
