//go:build !no_pg

// adr: 20261105102920
package migrations_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentRollbackReceiptPopulatedHistoryAndReplay(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	acct, err := store.CreateAccount(ctx, uuid.NewString()+"@receipt-migration.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "receipt-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	create := func(key string) state.ProjectEnvironmentPromotion {
		t.Helper()
		p, _, err := store.CreateProjectEnvironmentPromotion(ctx, state.ProjectEnvironmentPromotion{AccountID: acct.ID, ProjectID: project.ID, ProjectSlug: project.Slug, FromEnvironment: source.Slug, ToEnvironment: target.Slug, PromotionHash: strings.Repeat("a", 64), IdempotencyKey: key, Status: "succeeded"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	legacy := create("legacy-promotion")
	if _, err = store.StartProjectEnvironmentPromotionRollback(ctx, acct.ID, legacy.ID, "legacy-original"); err != nil {
		t.Fatal(err)
	}
	before, _, err := store.ProjectEnvironmentPromotionByID(ctx, acct.ID, project.Slug, target.Slug, legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	// This private clone now represents populated history immediately before
	// the new empty receipt table was introduced.
	if _, err = pool.Exec(ctx, `DROP TABLE project_environment_rollback_receipts; DELETE FROM goose_db_version WHERE version_id=20261105102920001`); err != nil {
		t.Fatal(err)
	}
	if err = db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	after, _, err := store.ProjectEnvironmentPromotionByID(ctx, acct.ID, project.Slug, target.Slug, legacy.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("migration changed existing rollback history", err)
	}
	legacyHash := api.EnvironmentRollbackRequestHash(project.ID, source.ID, target.ID, legacy.ID, legacy.PromotionHash)
	if _, err = store.StartProjectEnvironmentPromotionRollback(state.ContextWithEnvironmentRollbackAttempt(ctx, strings.Repeat("a", 32), legacyHash), acct.ID, legacy.ID, "legacy-original"); !errors.Is(err, state.ErrEnvironmentRollbackAttemptConflict) {
		t.Fatal("migration fabricated original acceptance", err)
	}
	fresh := create("exact-promotion")
	attempt := strings.Repeat("b", 32)
	hash := api.EnvironmentRollbackRequestHash(project.ID, source.ID, target.ID, fresh.ID, fresh.PromotionHash)
	if _, err = store.StartProjectEnvironmentPromotionRollback(state.ContextWithEnvironmentRollbackAttempt(ctx, attempt, hash), acct.ID, fresh.ID, "exact-original"); err != nil {
		t.Fatal(err)
	}
	receipt, err := store.EnvironmentRollbackReceipt(ctx, acct.ID, project.Slug, target.Slug, fresh.ID, attempt)
	if err != nil {
		t.Fatal(err)
	}
	// Replay against both existing objects and populated acceptance evidence.
	if _, err = pool.Exec(ctx, `DELETE FROM goose_db_version WHERE version_id=20261105102920001`); err != nil {
		t.Fatal(err)
	}
	if err = db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	replayed, err := store.EnvironmentRollbackReceipt(ctx, acct.ID, project.Slug, target.Slug, fresh.ID, attempt)
	if err != nil || !reflect.DeepEqual(receipt, replayed) {
		t.Fatal("replay changed original receipt", err)
	}
}
