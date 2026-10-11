package state_test

// adr: 689

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/state"
)

func waitRuntimeCutoverBlocked(t *testing.T, pool *pgxpool.Pool, blocker int32) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var blocked bool
		if err := pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))", blocker).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("cutover never waited on the retained fence")
}

func TestPgRuntimeUpgradeCutoverRechecksAfterWaitingForConfiguration(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	app, serving, candidate, r := runtimeUpgradeCutoverFixture(t, s, s)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var blocker int32
	if err := tx.QueryRow(t.Context(), "SELECT pg_backend_pid()").Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	// Updating the child locks its app parent through the existing publication
	// trigger. Cutover resolves owners first, then must wait and reread inputs.
	if _, err := tx.Exec(t.Context(), "UPDATE app_envs SET value='changed-while-waiting' WHERE app_id=$1 AND key='REGION'", app.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	go func() { _, err := s.CutoverDeploymentRuntimeUpgrade(ctx, r); done <- err }()
	waitRuntimeCutoverBlocked(t, pool, blocker)
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale pre-lock configuration authorized traffic", err)
	}
	assertRuntimeUpgradeTraffic(t, s, serving, candidate, false)
}

func TestPgRuntimeUpgradeCutoverRechecksAfterWaitingForRevocation(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	_, serving, candidate, r := runtimeUpgradeCutoverFixture(t, s, s)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var blocker int32
	if err := tx.QueryRow(t.Context(), "SELECT pg_backend_pid()").Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), "UPDATE runtime_release_qualifications SET revoked_at=clock_timestamp(),revocation_sha256=$2 WHERE release_id=$1",
		r.ExpectedTargetReleaseID, strings.Repeat("1", 64)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	go func() { _, err := s.CutoverDeploymentRuntimeUpgrade(ctx, r); done <- err }()
	waitRuntimeCutoverBlocked(t, pool, blocker)
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, state.ErrConflict) {
		t.Fatal("revocation crossed traffic transaction", err)
	}
	assertRuntimeUpgradeTraffic(t, s, serving, candidate, false)
}

func TestPgRuntimeUpgradeCutoverFailedWriteRollsBackAuthorization(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	_, serving, candidate, r := runtimeUpgradeCutoverFixture(t, s, s)
	// Fail a weight write after receipt insertion. Whichever deployment updates
	// first, the transaction must retain neither its weight nor authorization.
	if _, err := pool.Exec(t.Context(), `CREATE FUNCTION reject_cutover_weight() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN IF NEW.traffic_percent=100 AND EXISTS(SELECT 1 FROM deployment_runtime_upgrade_targets WHERE deployment_id=NEW.id) THEN
 RAISE EXCEPTION 'synthetic cutover write failure'; END IF; RETURN NEW; END $$;
CREATE TRIGGER reject_cutover_weight BEFORE UPDATE OF traffic_percent ON deployments FOR EACH ROW EXECUTE FUNCTION reject_cutover_weight();`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CutoverDeploymentRuntimeUpgrade(t.Context(), r); err == nil {
		t.Fatal("injected write did not fail")
	}
	assertRuntimeUpgradeTraffic(t, s, serving, candidate, false)
	if _, err := pool.Exec(t.Context(), "DROP TRIGGER reject_cutover_weight ON deployments; DROP FUNCTION reject_cutover_weight()"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CutoverDeploymentRuntimeUpgrade(t.Context(), r); err != nil {
		t.Fatal("failed attempt poisoned later cutover", err)
	}
	assertRuntimeUpgradeTraffic(t, s, serving, candidate, true)
}

func TestPgRuntimeUpgradeCutoverImmutableAndNoGenericSQLBypass(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	_, serving, candidate, r := runtimeUpgradeCutoverFixture(t, s, s)
	if _, err := pool.Exec(t.Context(), `INSERT INTO deployment_runtime_upgrade_targets
 (deployment_id,release_id,source_sha256,source_root,source_bytes,kind,handler)
 SELECT $1,release_id,source_sha256,source_root,source_bytes,kind,handler
 FROM deployment_runtime_upgrade_targets WHERE deployment_id=$2`, serving.ID, candidate.ID); err == nil {
		t.Fatal("serving row acquired a preparation pin after traffic")
	}
	for _, query := range []string{
		"UPDATE deployments SET traffic_percent=100 WHERE id=$1",
		"UPDATE deployments SET traffic_percent=CASE WHEN id=$1 THEN 100 ELSE 0 END WHERE app_id=(SELECT app_id FROM deployments WHERE id=$1)",
	} {
		if _, err := pool.Exec(t.Context(), query, candidate.ID); err == nil {
			t.Fatal("legacy SQL bypassed cutover", query)
		}
		assertRuntimeUpgradeTraffic(t, s, serving, candidate, false)
	}
	if _, err := s.CutoverDeploymentRuntimeUpgrade(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"UPDATE deployment_runtime_upgrade_cutovers SET cutover_at=cutover_at+interval '1 second' WHERE deployment_id=$1",
		"DELETE FROM deployment_runtime_upgrade_cutovers WHERE deployment_id=$1",
		"UPDATE deployments SET rootfs_key='borrowed.ext4' WHERE id=$1",
	} {
		if _, err := pool.Exec(t.Context(), query, candidate.ID); err == nil {
			t.Fatal("rewrote cutover authorization", query)
		}
	}
	assertRuntimeUpgradeTraffic(t, s, serving, candidate, true)
	// Parent deletion cascades operational evidence; instance lifetime is
	// independent and deleting the original VM cannot erase authorization.
	if _, err := pool.Exec(t.Context(), "DELETE FROM instances WHERE deployment_id=$1", candidate.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeploymentRuntimeUpgradeCutover(t.Context(), candidate.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "DELETE FROM deployments WHERE id=$1", candidate.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeploymentRuntimeUpgradeCutover(t.Context(), candidate.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("parent deletion retained orphan authorization", err)
	}
}

func TestPgRuntimeUpgradeCutoverLegacyRolloutCannotBypass(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	_, serving, candidate, _ := runtimeUpgradeCutoverFixture(t, s, s)
	if _, err := pool.Exec(t.Context(), `UPDATE deployments SET canary_preset='custom',canary_total_steps=2,canary_step=0,rollout_state='pending',
canary_stages='[ {"percent":25,"duration":"1m"},{"percent":100,"duration":"0s"} ]'::jsonb WHERE id=$1`, candidate.ID); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.AdvanceCanary(t.Context(), candidate.ID, state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 100})
	assertRuntimeUpgradeWriterFence(t, err)
	_, _, err = s.RecoverRollout(t.Context(), candidate.AppID, "promote", "synthetic bypass")
	assertRuntimeUpgradeWriterFence(t, err)
	assertRuntimeUpgradeTraffic(t, s, serving, candidate, false)
	if _, err := pool.Exec(t.Context(), "UPDATE deployments SET canary_total_steps=0,canary_preset='none',canary_stages=NULL,rollout_state='rolling_out' WHERE id=$1", candidate.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.FinalizeServiceRollout(t.Context(), candidate.ID)
	assertRuntimeUpgradeWriterFence(t, err)
	assertRuntimeUpgradeTraffic(t, s, serving, candidate, false)
}

func TestPgRuntimeUpgradeCutoverRejectsExpiredAcceptance(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	_, serving, candidate, r := runtimeUpgradeCutoverFixture(t, s, s)
	// Private synthetic fixture only: age immutable evidence without sleeping
	// fifteen minutes. Production has no refresh or timestamp mutation method.
	if _, err := pool.Exec(t.Context(), `ALTER TABLE deployment_runtime_upgrade_acceptances DISABLE TRIGGER runtime_upgrade_acceptance_immutable;
UPDATE deployment_runtime_upgrade_acceptances SET started_at=started_at-interval '16 minutes',ready_at=ready_at-interval '16 minutes';
ALTER TABLE deployment_runtime_upgrade_acceptances ENABLE TRIGGER runtime_upgrade_acceptance_immutable;`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CutoverDeploymentRuntimeUpgrade(t.Context(), r); !errors.Is(err, state.ErrConflict) {
		t.Fatal("expired readiness authorized traffic", err)
	}
	assertRuntimeUpgradeTraffic(t, s, serving, candidate, false)
}

func assertRuntimeUpgradeWriterFence(t *testing.T, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if errors.Is(err, state.ErrConflict) || (errors.As(err, &pgErr) && pgErr.ConstraintName == "runtime_upgrade_traffic_fenced") {
		return
	}
	t.Fatalf("writer did not reach the runtime upgrade fence: %v", err)
}
