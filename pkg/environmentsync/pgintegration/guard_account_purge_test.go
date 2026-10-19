package pgintegration_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// H10-94: an account purge cascades to both projects and the clone
// configuration guard, in trigger-name (OID) order. A test-local BEFORE DELETE
// trigger on accounts removes the guard first, pinning the losing order that
// made the ParentPurges/account tests flake: the project's BEFORE DELETE fence
// must accept the admitted purge, while a live project in a live account
// without its guard still fails closed.
func TestPgAccountPurgeToleratesGuardCascadedBeforeProject(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	lease, plan, _ := workloadGraphFixture(t, store)
	if _, err := store.PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan); err != nil {
		t.Fatal(err)
	}
	graph, err := store.ReconcileEnvironmentGitOpsPreparation(t.Context(), lease, plan)
	if err != nil {
		t.Fatal(err)
	}

	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `delete from project_environment_clone_configuration_guards where project_id=$1`, lease.Source.ProjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `select assert_clone_configuration_mutable($1)`, lease.Source.ProjectID); err == nil || !strings.Contains(err.Error(), "project configuration guard is missing") {
		t.Fatalf("live project without guard must fail closed: %v", err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(t.Context(), `
CREATE FUNCTION h10_94_drop_guard_first() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM project_environment_clone_configuration_guards WHERE account_id = OLD.id;
    RETURN OLD;
END;
$$;
CREATE TRIGGER h10_94_drop_guard_first BEFORE DELETE ON accounts
FOR EACH ROW EXECUTE FUNCTION h10_94_drop_guard_first();`); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAccountDeletionPending(t.Context(), lease.Source.AccountID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAccount(t.Context(), lease.Source.AccountID); err != nil {
		t.Fatalf("admitted account purge with guard cascaded first: %v", err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `select count(*) from environment_workload_graphs where id=$1`, graph.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("account purge retained graph: %d %v", count, err)
	}
}
