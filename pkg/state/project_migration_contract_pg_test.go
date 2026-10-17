//go:build !no_pg

package state

import (
	"context"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"testing"
	"time"
)

func TestMigrationContractPG(t *testing.T) {
	testMigrationContract(t, func(t *testing.T) Store { return NewPgStore(pgtest.OpenMigrated(t)) })
}

func TestMigrationContractPGRejectsWakeWaitingOnAdmission(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := NewPgStore(pool)
	f := newContractFixture(t, store)
	expireContractRetention(t, f)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	node, _ := store.ComputeNodeByName(ctx, DefaultLocalNodeName)
	parked, err := store.CreateInstance(ctx, f.apps[0].ID, f.old[0].DeploymentID, "parked", 256, node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	latch, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer latch.Release()
	if _, err = latch.Exec(ctx, "SELECT pg_advisory_lock(101701)"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = latch.Exec(context.Background(), "SELECT pg_advisory_unlock(101701)") }()
	if _, err = pool.Exec(ctx, `CREATE FUNCTION pause_contract_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.task_id IS NOT NULL THEN PERFORM pg_advisory_xact_lock(101701); END IF; RETURN NEW; END $$; CREATE TRIGGER pause_contract_commit BEFORE UPDATE ON project_migration_contracts FOR EACH ROW EXECUTE FUNCTION pause_contract_commit()`); err != nil {
		t.Fatal(err)
	}
	req, evidence := contractEvidence(f, t)
	admission := make(chan error, 1)
	go func() {
		_, err := store.AdmitProjectMigrationContract(ctx, f.account, f.attempt, req, evidence)
		admission <- err
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err = pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory')").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-admission:
			t.Fatalf("admission did not reach commit latch: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	wake := make(chan error, 1)
	go func() { wake <- store.UpdateInstanceState(ctx, parked.ID, "waking") }()

	for {
		var waiting bool
		if err = pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND wait_event<>'advisory' AND state='active')").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-wake:
			t.Fatalf("wake did not wait for admission: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	// Admission already owns the project lock; the wake must reread retirement
	// after waiting for that lock rather than reuse its statement's old snapshot.
	if _, err = latch.Exec(ctx, "SELECT pg_advisory_unlock(101701)"); err != nil {
		t.Fatal(err)
	}
	if err = <-admission; err != nil {
		t.Fatal(err)
	}
	if err = <-wake; err == nil {
		t.Fatal("stale wake succeeded after retirement committed")
	}
	var state string
	if err = pool.QueryRow(ctx, "SELECT state FROM instances WHERE id=$1", parked.ID).Scan(&state); err != nil || state != "parked" {
		t.Fatalf("retired instance state=%s err=%v", state, err)
	}
}
