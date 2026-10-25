// adr: 570
package pgintegration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPGEnvironmentGitOpsSecretReferenceCloneSerializationDeadlineAndRecovery(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	refs, source, _, app := secretRefFixture(t, state.NewPgStore(pool), "enforce")
	store := refs.(secretRefCloneStore)
	if err := store.UpsertAppEnvInScope(t.Context(), source.AccountID, app.ID, "production", "MODE", "production"); err != nil {
		t.Fatal(err)
	}
	// Inject the conflict after the catalog and plaintext copies. Sequence
	// increments survive rollback and establish that complete attempts retry.
	if _, err := pool.Exec(t.Context(), `
CREATE SEQUENCE clone_serialization_attempts;
CREATE FUNCTION fail_clone_serialization() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.scope='preview' THEN
  PERFORM nextval('clone_serialization_attempts');
  RAISE EXCEPTION 'test clone serialization failure' USING ERRCODE='40001';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER zz_fail_clone_serialization AFTER INSERT ON app_environment_secret_refs
 FOR EACH ROW EXECUTE FUNCTION fail_clone_serialization();`); err != nil {
		t.Fatal(err)
	}
	config := pool.Config().Copy()
	config.MaxConns = 1
	cloner, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer cloner.Close()
	clone := state.ProjectEnvironmentClone{AccountID: source.AccountID, ProjectID: source.ProjectID, SourceSlug: "production", TargetSlug: "preview"}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_, _, err = state.NewPgStore(cloner).CloneProjectEnvironment(ctx, clone, api.MustLimitsFor(api.PlanPro))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("persistent serialization error escaped caller deadline: %v", err)
	}
	var attempts int
	if err := pool.QueryRow(t.Context(), `select last_value from clone_serialization_attempts`).Scan(&attempts); err != nil || attempts < 2 {
		t.Fatalf("clone did not retry complete transactions: attempts=%d err=%v", attempts, err)
	}
	if _, err := store.ProjectEnvironmentBySlug(t.Context(), source.AccountID, source.ProjectID, "preview"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expired retries retained catalog: %v", err)
	}
	if count, err := store.CountAppEnvInScope(t.Context(), source.AccountID, app.ID, "preview"); err != nil || count != 0 {
		t.Fatalf("expired retries retained variables/references: %d %v", count, err)
	}
	if rows, err := store.ListAppSecretsInScope(t.Context(), source.AccountID, app.ID, "preview"); err != nil || len(rows) != 0 {
		t.Fatalf("expired retries retained sealed values: %d %v", len(rows), err)
	}
	var stamped bool
	if err := pool.QueryRow(t.Context(), `select exists(select 1 from app_runtime_config_scope_changes where app_id=$1 and scope='preview')`, app.ID).Scan(&stamped); err != nil || stamped {
		t.Fatalf("expired retries retained runtime stamp: %t %v", stamped, err)
	}
	// A canceled pgx query closes its connection asynchronously. Allow the
	// server to observe that close, but require a different physical pool to
	// acquire the account lock within the existing cleanup budget.
	probe, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Release()
	key := "gregale.traffic.account.v1:" + source.AccountID
	q := sqlc.New()
	cleanup, finish := context.WithTimeout(t.Context(), api.TrafficPolicyAnalysisTimeout)
	defer finish()
	for {
		locked, err := q.TryLockTrafficPolicySession(cleanup, probe, key)
		if err != nil {
			t.Fatalf("expired clone retained account session lock: %v", err)
		}
		if locked {
			break
		}
		select {
		case <-cleanup.Done():
			t.Fatalf("expired clone retained account session lock: %v", cleanup.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if unlocked, err := q.UnlockTrafficPolicySession(t.Context(), probe, key); err != nil || !unlocked {
		t.Fatalf("release probe lock: unlocked=%t err=%v", unlocked, err)
	}
	if _, err := pool.Exec(t.Context(), `drop trigger zz_fail_clone_serialization on app_environment_secret_refs`); err != nil {
		t.Fatal(err)
	}
	recovery, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	if _, result, err := state.NewPgStore(cloner).CloneProjectEnvironment(recovery, clone, api.MustLimitsFor(api.PlanPro)); err != nil || result.SecretReferencesCopied != 1 || result.VariablesCopied != 1 {
		t.Fatalf("single-connection clone recovery: %+v %v", result, err)
	}
	assertSecretRef(t, refs, source, app, "preview", "DATABASE_URL", "secret:DATABASE_A")
}
