package pgintegration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPGEnvironmentGitOpsSecretReferenceCloneFailureRollsBackAllRows(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	refs, source, _, app := secretRefFixture(t, state.NewPgStore(pool), "enforce")
	store := refs.(secretRefCloneStore)
	if err := store.UpsertAppEnvInScope(t.Context(), source.AccountID, app.ID, "production", "MODE", "production"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `
CREATE FUNCTION fail_clone_reference_insert() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.scope='preview' THEN RAISE EXCEPTION 'test clone failure after value copy'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER zz_fail_clone_reference BEFORE INSERT ON app_environment_secret_refs
 FOR EACH ROW EXECUTE FUNCTION fail_clone_reference_insert();`); err != nil {
		t.Fatal(err)
	}
	clone := state.ProjectEnvironmentClone{AccountID: source.AccountID, ProjectID: source.ProjectID, SourceSlug: "production", TargetSlug: "preview"}
	if _, _, err := store.CloneProjectEnvironment(t.Context(), clone, api.MustLimitsFor(api.PlanPro)); err == nil {
		t.Fatal("reference copy failure accepted")
	}
	if _, err := store.ProjectEnvironmentBySlug(t.Context(), source.AccountID, source.ProjectID, "preview"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("failed clone retained catalog: %v", err)
	}
	if rows, err := store.ListAppSecretsInScope(t.Context(), source.AccountID, app.ID, "preview"); err != nil || len(rows) != 0 {
		t.Fatalf("failed clone retained sealed values: %d %v", len(rows), err)
	}
	if count, err := store.CountAppEnvInScope(t.Context(), source.AccountID, app.ID, "preview"); err != nil || count != 0 {
		t.Fatalf("failed clone retained variables/references: %d %v", count, err)
	}
	var stamped bool
	if err := pool.QueryRow(t.Context(), `select exists(select 1 from app_runtime_config_scope_changes where app_id=$1 and scope='preview')`, app.ID).Scan(&stamped); err != nil || stamped {
		t.Fatalf("failed clone retained runtime stamp: %t %v", stamped, err)
	}
	if _, err := pool.Exec(t.Context(), `drop trigger zz_fail_clone_reference on app_environment_secret_refs`); err != nil {
		t.Fatal(err)
	}
	if _, result, err := store.CloneProjectEnvironment(t.Context(), clone, api.MustLimitsFor(api.PlanPro)); err != nil || result.SecretReferencesCopied != 1 || result.VariablesCopied != 1 {
		t.Fatalf("clone recovery: %+v %v", result, err)
	}
}

func TestPGEnvironmentGitOpsSecretReferenceCloneWaitsForSourceWrite(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	refs, source, _, app := secretRefFixture(t, state.NewPgStore(pool), "enforce")
	holder, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(context.Background()) }()
	// The write holds its source/app locks and leaves DATABASE_B uncommitted.
	if _, err := holder.Exec(t.Context(), `update app_environment_secret_refs set secret_name='DATABASE_B' where app_id=$1 and scope='production'`, app.ID); err != nil {
		t.Fatal(err)
	}
	config := pool.Config().Copy()
	name := "gitops-secret-reference-clone"
	config.ConnConfig.RuntimeParams["application_name"] = name
	cloner, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer cloner.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, _, err := state.NewPgStore(cloner).CloneProjectEnvironment(ctx, state.ProjectEnvironmentClone{
			AccountID: source.AccountID, ProjectID: source.ProjectID, SourceSlug: "production", TargetSlug: "preview",
		}, api.MustLimitsFor(api.PlanPro))
		result <- err
	}()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `select exists(select 1 from pg_stat_activity where application_name=$1 and wait_event_type='Lock')`, name).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("clone bypassed pending source write: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	assertSecretRef(t, refs, source, app, "preview", "DATABASE_URL", "secret:DATABASE_B")
	assertSecretRef(t, refs, source, app, "production", "DATABASE_URL", "secret:DATABASE_B")
}
