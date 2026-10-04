//go:build !no_pg

// adr: 583
package state_test

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestPgEnvironmentQueueAdmissionIsOwnedPinnedAndPrivate(t *testing.T) {
	s, _, _ := pgWithPool(t)
	testEnvironmentQueueAdmission(t, s)
}
func TestPgEnvironmentQueueClaimsShareConcurrencyAndQuota(t *testing.T) {
	s, _, _ := pgWithPool(t)
	testEnvironmentQueueClaims(t, s)
}
func TestPgEnvironmentQueueMessagesCleanUpAfterRetirement(t *testing.T) {
	s, _, _ := pgWithPool(t)
	testEnvironmentQueueMessageCleanup(t, s)
}

func TestPgEnvironmentQueueAdmissionHonorsDeletionLockAndRollsBack(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	f := seedQueueConsumers(t, s)
	if _, err := s.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, f.dep.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT id FROM project_environments WHERE id=$1 FOR UPDATE`, f.spec.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	blocked, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	_, err = s.EnqueueProjectEnvironmentQueueInvocation(blocked, f.account.ID, f.project.ID, f.dep.ID, "orders", state.Invocation{})
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("admission bypassed environment deletion lock: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_queue_admission() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'injected admission failure'; END; $$ LANGUAGE plpgsql;
        CREATE TRIGGER fail_queue_admission BEFORE INSERT ON invocation_environment_queue_admissions FOR EACH ROW EXECUTE FUNCTION fail_queue_admission()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueueProjectEnvironmentQueueInvocation(ctx, f.account.ID, f.project.ID, f.dep.ID, "orders", state.Invocation{}); err == nil {
		t.Fatal("injected proof insertion succeeded")
	}
	var messages, proofs int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM invocations WHERE app_id=$1),(SELECT count(*) FROM invocation_environment_queue_admissions)`, f.app.ID).Scan(&messages, &proofs); err != nil || messages != 0 || proofs != 0 {
		t.Fatalf("partial queue admission: %d %d %v", messages, proofs, err)
	}
}

func TestPgEnvironmentQueueClaimsRejectCorruptedOwnershipWithoutQuota(t *testing.T) {
	for _, change := range []string{
		`UPDATE invocation_environment_queue_admissions SET pin_hash=repeat('0',64) WHERE invocation_id=$1`,
		`UPDATE invocation_environment_queue_admissions SET definition_hash=repeat('0',64) WHERE invocation_id=$1`,
		`UPDATE invocations SET environment_id=NULL WHERE id=$1`,
		`UPDATE invocations SET source='async_invoke',queue_name='' WHERE id=$1`,
		`DELETE FROM invocation_environment_queue_admissions WHERE invocation_id=$1`,
		`UPDATE invocations SET headers=headers||jsonb_build_object('x-gregale-revision','bogus') WHERE id=$1`,
		`UPDATE project_environment_queue_consumers SET definition=jsonb_set(definition::jsonb,'{max_concurrency}','10')::json WHERE id=(SELECT consumer_id FROM invocation_environment_queue_admissions WHERE invocation_id=$1)`,
	} {
		t.Run(change[:25], func(t *testing.T) {
			s, ctx, pool := pgWithPool(t)
			f := seedQueueConsumers(t, s)
			if _, err := s.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, f.dep.ID); err != nil {
				t.Fatal(err)
			}
			inv := enqueueStageQueue(t.Context(), t, s, f)
			if _, err := pool.Exec(ctx, change, inv.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ClaimInvocationWithCap(ctx, inv.ID, "", 30, 4); !errors.Is(err, state.ErrInvocationEnvironmentWorkIsolation) {
				t.Fatalf("corrupt proof claimed: %v", err)
			}
			row, err := s.InvocationByID(ctx, inv.ID)
			if err != nil || row.Attempts != 0 || row.QuotaReserved || row.State != state.InvocationPending {
				t.Fatalf("rejected ownership mutated row: %+v %v", row, err)
			}
			if _, _, err := s.GetAccountAsyncQuota(ctx, f.account.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("corrupt admission reserved account quota: %v", err)
			}
			if err := s.UpdateDeploymentStatus(ctx, f.dep.ID, state.DeploySuperseded, ""); err != nil {
				t.Fatal(err)
			}
			if err := s.DeleteProjectEnvironment(ctx, f.account.ID, f.project.ID, "stage"); !errors.Is(err, state.ErrInvocationEnvironmentWorkIsolation) {
				t.Fatalf("corrupt evidence deleted: %v", err)
			}
		})
	}
}

func TestPgEnvironmentQueueAdmissionSupportsEveryDefinitionClassAndMode(t *testing.T) {
	s, _, _ := pgWithPool(t)
	testEnvironmentQueueAdmissionClasses(t, s)
}

func TestPgEnvironmentQueuePartitionsShareOnlyAccountQuota(t *testing.T) {
	s, _, _ := pgWithPool(t)
	testEnvironmentQueuePartitions(t, s)
}
