package state_test

// adr: 690

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgRuntimeUpgradeOperationCheckpointFailureRollsBackQueueAndCutover(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	app, serving, candidate, r := runtimeUpgradeOperationFixture(t, s, s)
	if _, err := s.RegisterRuntimeUpgradeOperation(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `CREATE FUNCTION reject_upgrade_checkpoint() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.phase<>OLD.phase THEN RAISE EXCEPTION 'synthetic checkpoint failure'; END IF; RETURN NEW; END $$;
CREATE TRIGGER reject_upgrade_checkpoint BEFORE UPDATE ON runtime_upgrade_operations FOR EACH ROW EXECUTE FUNCTION reject_upgrade_checkpoint();`); err != nil {
		t.Fatal(err)
	}
	claim := claimRuntimeUpgradeOperation(t, s)
	if _, err := s.AdvanceRuntimeUpgradeOperation(t.Context(), claim); err == nil {
		t.Fatal("checkpoint failure ignored")
	}
	if _, err := s.BuildByDeployment(t.Context(), candidate.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("queue escaped failed checkpoint", err)
	}
	op, err := s.RuntimeUpgradeOperation(t.Context(), app.AccountID, r.ID)
	if err != nil || op.Phase != state.RuntimeUpgradePrepared {
		t.Fatal(op, err)
	}
	if _, err := pool.Exec(t.Context(), `DROP TRIGGER reject_upgrade_checkpoint ON runtime_upgrade_operations`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE runtime_upgrade_operations SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, r.ID); err != nil {
		t.Fatal(err)
	}
	claim = claimRuntimeUpgradeOperation(t, s)
	if _, err := s.AdvanceRuntimeUpgradeOperation(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	wake := runtimeUpgradeOperationReady(t, s, s, app, candidate, r)
	if _, err := pool.Exec(t.Context(), `CREATE TRIGGER reject_upgrade_checkpoint BEFORE UPDATE ON runtime_upgrade_operations FOR EACH ROW EXECUTE FUNCTION reject_upgrade_checkpoint()`); err != nil {
		t.Fatal(err)
	}
	claim = claimRuntimeUpgradeOperation(t, s)
	if _, err := s.AdvanceRuntimeUpgradeOperation(t.Context(), claim); err == nil {
		t.Fatal("completion checkpoint failure ignored")
	}
	assertRuntimeUpgradeTraffic(t, s, serving, candidate, false)
	op, err = s.RuntimeUpgradeOperation(t.Context(), app.AccountID, r.ID)
	if err != nil || op.Phase != state.RuntimeUpgradeWaiting || op.WakeID != "" {
		t.Fatal(op, err)
	}
	if _, err := pool.Exec(t.Context(), `DROP TRIGGER reject_upgrade_checkpoint ON runtime_upgrade_operations`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE runtime_upgrade_operations SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, r.ID); err != nil {
		t.Fatal(err)
	}
	claim = claimRuntimeUpgradeOperation(t, s)
	complete, err := s.AdvanceRuntimeUpgradeOperation(t.Context(), claim)
	if err != nil || complete.Phase != state.RuntimeUpgradeComplete || complete.WakeID != wake {
		t.Fatal(complete, err)
	}
	assertRuntimeUpgradeTraffic(t, s, serving, candidate, true)
	// The SQL guard requires a real cutover, then makes terminal history immutable.
	if _, err := pool.Exec(t.Context(), `UPDATE runtime_upgrade_operations SET phase='prepared',wake_id=NULL,finished_at=NULL WHERE id=$1`, r.ID); err == nil {
		t.Fatal("terminal operation reopened")
	}
}

func TestPgRuntimeUpgradeOperationLeaseExpiryDuringLockWaitRollsBackBuild(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	app, _, candidate, r := runtimeUpgradeOperationFixture(t, s, s)
	if _, err := s.RegisterRuntimeUpgradeOperation(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	claim := claimRuntimeUpgradeOperation(t, s)
	if _, err := pool.Exec(t.Context(), `UPDATE runtime_upgrade_operations SET lease_until=clock_timestamp()+interval '300 milliseconds' WHERE id=$1`, r.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var blocker int32
	if err := tx.QueryRow(t.Context(), `SELECT pg_backend_pid() FROM apps WHERE id=$1 FOR UPDATE`, app.ID).Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	go func() { _, err := s.AdvanceRuntimeUpgradeOperation(ctx, claim); done <- err }()
	waitRuntimeCutoverBlocked(t, pool, blocker)
	time.Sleep(350 * time.Millisecond)
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, state.ErrConflict) {
		t.Fatal("expired lease authorized work", err)
	}
	if _, err := s.BuildByDeployment(t.Context(), candidate.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("expired queue committed", err)
	}
	op, err := s.RuntimeUpgradeOperation(t.Context(), app.AccountID, r.ID)
	if err != nil || op.Phase != state.RuntimeUpgradePrepared {
		t.Fatal(op, err)
	}
	fresh := claimRuntimeUpgradeOperation(t, s)
	if _, err := s.AdvanceRuntimeUpgradeOperation(t.Context(), fresh); err != nil {
		t.Fatal(err)
	}
}

func TestPgRuntimeUpgradeOperationDeadlineAndRawIntentFences(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	app, serving, candidate, r := runtimeUpgradeOperationFixture(t, s, s)
	if _, err := s.RegisterRuntimeUpgradeOperation(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE runtime_upgrade_operations SET source_sha256=repeat('e',64) WHERE id=$1`, r.ID); err == nil {
		t.Fatal("reviewed intent replaced")
	}
	if _, err := pool.Exec(t.Context(), `UPDATE runtime_upgrade_operations SET phase='complete',wake_id=gen_random_uuid(),finished_at=now(),lease_token=NULL,lease_until=NULL WHERE id=$1`, r.ID); err == nil {
		t.Fatal("completion without cutover")
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO runtime_upgrade_operations(id,account_id,app_id,deployment_id,serving_deployment_id,target_release_id,source_sha256,qualification_report_sha256,phase,wake_id,deadline_at,finished_at)
SELECT gen_random_uuid(),account_id,app_id,deployment_id,serving_deployment_id,target_release_id,source_sha256,qualification_report_sha256,'complete',gen_random_uuid(),deadline_at,now() FROM runtime_upgrade_operations WHERE id=$1`, r.ID); err == nil || !strings.Contains(err.Error(), "completion requires retained cutover") {
		t.Fatal("insert bypassed completion evidence fence", err)
	}
	// Age only the synthetic fixture; normal writers cannot alter its deadline.
	if _, err := pool.Exec(t.Context(), `ALTER TABLE runtime_upgrade_operations DISABLE TRIGGER runtime_upgrade_operation_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE runtime_upgrade_operations SET created_at=now()-interval '2 hours',deadline_at=now()-interval '1 hour' WHERE id=$1`, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `ALTER TABLE runtime_upgrade_operations ENABLE TRIGGER runtime_upgrade_operation_guard`); err != nil {
		t.Fatal(err)
	}
	claim := claimRuntimeUpgradeOperation(t, s)
	op, err := s.AdvanceRuntimeUpgradeOperation(t.Context(), claim)
	if err != nil || op.Phase != state.RuntimeUpgradeBlocked || op.Blocker != "deadline_exceeded" {
		t.Fatal(op, err)
	}
	if _, err := s.BuildByDeployment(t.Context(), candidate.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("expired operation queued", err)
	}
	stable, err := s.DeploymentByID(t.Context(), serving.ID)
	if err != nil || stable.TrafficPercent != 100 {
		t.Fatal(stable, err)
	}
	if _, err := s.RuntimeUpgradeOperation(t.Context(), app.AccountID, r.ID); err != nil {
		t.Fatal(err)
	}
}
