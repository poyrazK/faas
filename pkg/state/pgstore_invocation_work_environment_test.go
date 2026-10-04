//go:build !no_pg

// adr: 569
package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgInvocationWorkEnvironmentIsolation(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testInvocationWorkEnvironmentIsolation(t, store)
}

func TestPgInvocationWorkEnvironmentCleanup(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testInvocationWorkEnvironmentCleanup(t, store)
}

func TestPgInvocationWorkEnvironmentClaimFailsClosed(t *testing.T) {
	for _, fault := range []struct{ name, sql string }{
		{"missing_admission", "DELETE FROM invocation_work_environment_admissions WHERE invocation_id=$1"},
		{"missing_key_owner", "DELETE FROM invocation_work_environment_domains WHERE app_id=$2 AND kind='key'"},
		{"missing_fairness_owner", "DELETE FROM invocation_work_environment_domains WHERE app_id=$2 AND kind='fairness'"},
		{"missing_both", "WITH removed AS (DELETE FROM invocation_work_environment_admissions WHERE invocation_id=$1) DELETE FROM invocation_work_environment_domains WHERE app_id=$2"},
		{"missing_pin", "UPDATE invocations SET headers='{}'::jsonb WHERE id=$1"},
		{"changed_revision", "UPDATE invocations SET work_policy_revision=work_policy_revision+1 WHERE id=$1"},
		{"changed_key", "UPDATE invocations SET work_key_digest=decode(repeat('ff',32),'hex') WHERE id=$1"},
		{"changed_fairness", "UPDATE invocations SET work_fairness_limit=2 WHERE id=$1"},
	} {
		t.Run(fault.name, func(t *testing.T) {
			store, ctx, pool := pgWithPool(t)
			f := seedInvocationWorkEnvironment(t, store)
			row, err := store.EnqueueKeyedInvocation(ctx, f.request(t.Context(), t, store, "staging"), f.serial, "s:first", "s:customer")
			if err != nil {
				t.Fatal(err)
			}
			query := fault.sql
			if fault.name == "missing_key_owner" || fault.name == "missing_fairness_owner" {
				query += " AND EXISTS(SELECT 1 FROM invocations WHERE id=$1)"
				_, err = pool.Exec(ctx, query, row.ID, f.app.ID)
			} else if fault.name == "missing_both" {
				_, err = pool.Exec(ctx, query, row.ID, f.app.ID)
			} else {
				_, err = pool.Exec(ctx, query, row.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.ClaimInvocationWithCap(ctx, row.ID, "", 30, 100); !errors.Is(err, state.ErrInvocationEnvironmentWorkIsolation) {
				t.Fatalf("corrupt stage work claimed: %v", err)
			}
			if actual, err := store.InvocationByID(ctx, row.ID); err != nil || actual.State != state.InvocationPending || actual.Attempts != 0 || actual.QuotaReserved {
				t.Fatalf("rejected claim mutated work: %+v, %v", actual, err)
			}
		})
	}
}
