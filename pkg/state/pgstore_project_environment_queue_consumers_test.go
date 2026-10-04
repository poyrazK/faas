//go:build !no_pg

// adr: 531
package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgEnvironmentQueuePreparationHonorsEnvironmentDeletionLock(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	f := seedQueueConsumers(t, store)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT id FROM project_environments WHERE id=$1 FOR UPDATE`, f.spec.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	blocked, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	_, err = store.PrepareProjectEnvironmentQueueConsumers(blocked, f.account.ID, f.project.ID, f.dep.ID)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("consumer preparation bypassed deletion ownership lock: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM project_environment_queue_runtime_sets`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("blocked preparation wrote a runtime set: %d, %v", count, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, f.dep.ID); err != nil {
		t.Fatalf("retry after deletion lock released: %v", err)
	}
}

func TestPgEnvironmentQueueConsumersAreCompleteOwnedAndPinned(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testEnvironmentQueueConsumers(t, store)
}

func TestPgEnvironmentQueueConsumersEmptyAndUnavailable(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testEnvironmentQueueConsumersEmptyAndUnavailable(t, store)
}

func TestPgEnvironmentQueueConsumerCleanup(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	f := testEnvironmentQueueConsumerCleanup(t, store)
	var sets, consumers int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM project_environment_queue_runtime_sets WHERE app_id=$1),
		(SELECT count(*) FROM project_environment_queue_consumers)`, f.app.ID).Scan(&sets, &consumers); err != nil || sets != 0 || consumers != 0 {
		t.Fatalf("deleted environment left operational consumer rows: sets=%d consumers=%d, %v", sets, consumers, err)
	}
}

func TestPgEnvironmentQueueConsumerPreparationIsAtomic(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	f := seedQueueConsumers(t, store)
	// Fail the second insert after the first consumer and parent were written.
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_queue_consumer_prepare() RETURNS trigger AS $$
		BEGIN IF NEW.name = 'retry' THEN RAISE EXCEPTION 'injected consumer failure'; END IF; RETURN NEW; END;
		$$ LANGUAGE plpgsql;
		CREATE TRIGGER fail_queue_consumer_prepare BEFORE INSERT ON project_environment_queue_consumers
		FOR EACH ROW EXECUTE FUNCTION fail_queue_consumer_prepare()`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, f.dep.ID); err == nil {
		t.Fatal("partial preparation succeeded")
	}
	var parents, consumers int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM project_environment_queue_runtime_sets),
		(SELECT count(*) FROM project_environment_queue_consumers)`).Scan(&parents, &consumers); err != nil || parents != 0 || consumers != 0 {
		t.Fatalf("partial projection persisted: parents=%d consumers=%d, %v", parents, consumers, err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER fail_queue_consumer_prepare ON project_environment_queue_consumers;
		DROP FUNCTION fail_queue_consumer_prepare()`); err != nil {
		t.Fatal(err)
	}
	set, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, f.dep.ID)
	if err != nil || len(set.Consumers) != 2 {
		t.Fatalf("retry after rollback: %+v, %v", set, err)
	}
}

func TestPgEnvironmentQueueConsumerProofRejectsDamage(t *testing.T) {
	for _, fault := range []struct{ name, query string }{
		{"missing_consumer", `DELETE FROM project_environment_queue_consumers WHERE id=$1::uuid`},
		{"changed_definition", `UPDATE project_environment_queue_consumers SET definition=jsonb_set(definition::jsonb,'{max_concurrency}','99')::json WHERE id=$1::uuid`},
		{"unknown_definition_field", `UPDATE project_environment_queue_consumers SET definition=(definition::jsonb || '{"unexpected":true}'::jsonb)::json WHERE id=$1::uuid`},
		{"changed_definition_hash", `UPDATE project_environment_queue_consumers SET definition_hash=repeat('0',64) WHERE id=$1::uuid`},
		{"changed_settings_hash", `UPDATE project_environment_queue_runtime_sets SET settings_hash=repeat('0',64) WHERE id=$1::uuid`},
		{"changed_queue_revision", `UPDATE project_environment_queue_runtime_sets SET queue_revision=queue_revision+1 WHERE id=$1::uuid`},
		{"changed_book_hash", `UPDATE project_environment_queue_runtime_sets SET book_hash=repeat('0',64) WHERE id=$1::uuid`},
		{"changed_count", `UPDATE project_environment_queue_runtime_sets SET binding_count=0 WHERE id=$1::uuid`},
		{"foreign_owner", `UPDATE project_environment_queue_runtime_sets SET environment_id=$2::uuid WHERE id=$1::uuid`},
		{"unknown_pinned_setting", `UPDATE project_environment_workload_specs SET settings=(settings::jsonb || '{"unexpected":true}'::jsonb)::json WHERE id=$1::uuid`},
		{"changed_pinned_setting", `UPDATE project_environment_workload_specs SET settings=jsonb_set(settings::jsonb,'{ram_mb}','512')::json WHERE id=$1::uuid`},
	} {
		t.Run(fault.name, func(t *testing.T) {
			store, ctx, pool := pgWithPool(t)
			f := seedQueueConsumers(t, store)
			set, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, f.dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			id := set.ID
			if fault.name == "missing_consumer" || fault.name == "changed_definition" || fault.name == "changed_definition_hash" || fault.name == "unknown_definition_field" {
				id = set.Consumers[0].ID
			}
			if fault.name == "unknown_pinned_setting" || fault.name == "changed_pinned_setting" {
				id = f.spec.ID
			}
			args := []any{id}
			if fault.name == "foreign_owner" {
				other, err := store.ProjectEnvironmentBySlug(ctx, f.account.ID, f.project.ID, "other")
				if err != nil {
					t.Fatal(err)
				}
				args = append(args, other.ID)
			}
			if _, err := pool.Exec(ctx, fault.query, args...); err != nil {
				t.Fatal(err)
			}
			if _, err := store.ProjectEnvironmentQueueConsumersForDeployment(ctx, f.account.ID, f.project.ID, f.dep.ID); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("damaged preparation read: %v", err)
			}
			if _, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, f.dep.ID); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("damaged preparation repaired instead of refused: %v", err)
			}
		})
	}
}
