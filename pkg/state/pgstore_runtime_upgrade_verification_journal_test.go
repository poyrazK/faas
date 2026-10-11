package state_test

// adr: 694
// adr: 695

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runtimeupgrade"
	"github.com/onebox-faas/faas/pkg/state"
)

func verificationJournalAgeCutover(t *testing.T, pool *pgxpool.Pool, id string, age time.Duration) {
	t.Helper()
	// Move only synthetic immutable history to exercise observation/deadline
	// windows without sleeping thirty minutes. This is no native VM evidence.
	if _, err := pool.Exec(t.Context(), `ALTER TABLE deployment_runtime_upgrade_cutovers DISABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE deployment_runtime_upgrade_cutovers SET cutover_at=clock_timestamp()-make_interval(secs=>$2) WHERE deployment_id=$1`, id, int(age/time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `ALTER TABLE deployment_runtime_upgrade_cutovers ENABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
}

func verificationJournalHealthyEvidence(t *testing.T, s *state.PgStore, app, candidate string) {
	t.Helper()
	now := time.Now().UTC()
	claim, err := s.ClaimAppHealth(t.Context(), uuid.NewString(), now)
	if err != nil || claim.AppID != app {
		t.Fatal(claim, err)
	}
	h := api.AppHealthResponse{AppID: app, Scope: "default", Status: "healthy", Phase: "serving", Summary: "Synthetic scoped health",
		EvaluatedAt: now.Format(time.RFC3339Nano), ValidForSeconds: 120, MetricsAsOf: now.Format(time.RFC3339Nano),
		ServingDeploymentIDs: []string{candidate}, LatestDeploymentID: candidate, Capacity: api.AppHealthCapacity{Known: true, Required: 1, Ready: 1},
		Requests: &api.AppHealthRequests{Known: true, Coverage: "serving_deployments", WindowSeconds: 300, DeploymentIDs: []string{candidate}, RequestCount: 100}}
	if err := s.FinishAppHealth(t.Context(), claim, h, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

func verificationJournalDue(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `UPDATE runtime_upgrade_verifications SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE operation_id=$1`, id); err != nil {
		t.Fatal(err)
	}
}

func TestPgRuntimeUpgradeVerificationJournalRecoversAndRetainsHistoricalSuccess(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	app, serving, candidate, r := completeVerificationFixture(t, s)
	verificationJournalAgeCutover(t, pool, candidate.ID, 6*time.Minute)
	sessions := []string{uuid.NewString(), uuid.NewString()}
	seedReviewedRuntimeUpgradeGateways(t, s, sessions)
	j, err := s.StartRuntimeUpgradeVerification(t.Context(), r.AccountID, r.ID, sessions)
	if err != nil {
		t.Fatal(err)
	}
	lost, err := s.ClaimRuntimeUpgradeVerification(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Simulate process death and lease expiry, without carrying process memory.
	if _, err := pool.Exec(t.Context(), `UPDATE runtime_upgrade_verifications SET lease_until=clock_timestamp()-interval '1 second' WHERE operation_id=$1`, r.ID); err != nil {
		t.Fatal(err)
	}
	restarted := state.NewPgStore(pool)
	claim, err := restarted.ClaimRuntimeUpgradeVerification(t.Context())
	if err != nil || claim.LeaseToken == lost.LeaseToken {
		t.Fatal("restart reused lease", claim, err)
	}
	if _, err := restarted.AdvanceRuntimeUpgradeVerification(t.Context(), lost); !errors.Is(err, state.ErrConflict) {
		t.Fatal("abandoned worker checkpointed", err)
	}
	if err := restarted.RecordRuntimeUpgradeGateway(t.Context(), app.ID, sessions[0], candidate.ID); err != nil {
		t.Fatal(err)
	}
	pending, err := restarted.AdvanceRuntimeUpgradeVerification(t.Context(), claim)
	if err != nil || pending.LastObservation == nil || pending.LastObservation.ConfirmedGateways != 1 || pending.Reason != "gateway_confirmation_pending" {
		t.Fatal(pending, err)
	}
	if err := restarted.RecordRuntimeUpgradeGateway(t.Context(), app.ID, sessions[1], candidate.ID); err != nil {
		t.Fatal(err)
	}
	verificationJournalHealthyEvidence(t, restarted, app.ID, candidate.ID)
	verificationJournalDue(t, pool, r.ID)
	if did, err := (runtimeupgrade.VerificationExecutor{Store: restarted}).RunOnce(t.Context()); err != nil || !did {
		t.Fatal(did, err)
	}
	verified, err := restarted.RuntimeUpgradeVerificationJournal(t.Context(), r.AccountID, r.ID)
	if err != nil || verified.Phase != state.RuntimeUpgradeVerificationVerified || verified.FinishedAt.IsZero() || verified.LastObservation.Status != "verified" || verified.LastObservation.ConfirmedGateways != 2 || !verified.DeadlineAt.Equal(j.DeadlineAt) {
		t.Fatal(verified, err)
	}
	if _, err := restarted.ClaimRuntimeUpgradeVerification(t.Context()); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("terminal evidence reclaimed", err)
	}
	for _, sql := range []string{
		`UPDATE runtime_upgrade_verifications SET gateway_sessions=ARRAY[gen_random_uuid()] WHERE operation_id=$1`,
		`UPDATE runtime_upgrade_verifications SET deadline_at=deadline_at+interval '1 minute' WHERE operation_id=$1`,
		`UPDATE runtime_upgrade_verifications SET last_observation='{}'::jsonb WHERE operation_id=$1`,
	} {
		if _, err := pool.Exec(t.Context(), sql, r.ID); err == nil {
			t.Fatal("database permitted terminal/intent mutation", sql)
		}
	}
	if _, err := s.UpdateDeploymentTraffic(t.Context(), serving.ID, 100, candidate.ID); err != nil {
		t.Fatal(err)
	}
	fresh, err := restarted.VerifyRuntimeUpgrade(t.Context(), r.AccountID, r.ID, sessions)
	if err != nil || fresh.Status == "verified" || fresh.Reason != "activation_inputs_changed" {
		t.Fatal("historical journal became current proof", fresh, err)
	}
	replay, err := restarted.StartRuntimeUpgradeVerification(t.Context(), r.AccountID, r.ID, sessions)
	if err != nil || !reflect.DeepEqual(replay, verified) {
		t.Fatal("rollback rewrote or reopened verification history", replay, err)
	}
}

func TestPgRuntimeUpgradeVerificationJournalExpiresWithoutExtendingDeadline(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	_, _, candidate, r := completeVerificationFixture(t, s)
	verificationJournalAgeCutover(t, pool, candidate.ID, 31*time.Minute)
	sessions := []string{uuid.NewString()}
	seedReviewedRuntimeUpgradeGateways(t, s, sessions)
	j, err := s.StartRuntimeUpgradeVerification(t.Context(), r.AccountID, r.ID, sessions)
	if err != nil || j.DeadlineAt.After(time.Now()) {
		t.Fatal(j, err)
	}
	if _, err := (runtimeupgrade.VerificationExecutor{Store: s}).RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	expired, err := s.RuntimeUpgradeVerificationJournal(t.Context(), r.AccountID, r.ID)
	if err != nil || expired.Phase != state.RuntimeUpgradeVerificationExpired || expired.Reason != "deadline_exceeded" || !expired.DeadlineAt.Equal(j.DeadlineAt) {
		t.Fatal(expired, err)
	}
	replay, err := s.StartRuntimeUpgradeVerification(t.Context(), r.AccountID, r.ID, sessions)
	if err != nil || !reflect.DeepEqual(expired, replay) {
		t.Fatal("retry extended expired review", replay, err)
	}
}

func TestPgRuntimeUpgradeVerificationJournalBlocksRollback(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	_, serving, candidate, r := completeVerificationFixture(t, s)
	sessions := []string{uuid.NewString()}
	seedReviewedRuntimeUpgradeGateways(t, s, sessions)
	if _, err := s.StartRuntimeUpgradeVerification(t.Context(), r.AccountID, r.ID, sessions); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateDeploymentTraffic(t.Context(), serving.ID, 100, candidate.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := (runtimeupgrade.VerificationExecutor{Store: state.NewPgStore(pool)}).RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	j, err := s.RuntimeUpgradeVerificationJournal(t.Context(), r.AccountID, r.ID)
	if err != nil || j.Phase != state.RuntimeUpgradeVerificationBlocked || j.Reason != "activation_inputs_changed" {
		t.Fatal(j, err)
	}
}

func TestPgRuntimeUpgradeVerificationJournalLeaseExpiryAtAppFencePublishesNothing(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	app, _, _, r := completeVerificationFixture(t, s)
	sessions := []string{uuid.NewString()}
	seedReviewedRuntimeUpgradeGateways(t, s, sessions)
	if _, err := s.StartRuntimeUpgradeVerification(t.Context(), r.AccountID, r.ID, sessions); err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimRuntimeUpgradeVerification(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE runtime_upgrade_verifications SET lease_until=clock_timestamp()+interval '2 seconds' WHERE operation_id=$1`, r.ID); err != nil {
		t.Fatal(err)
	}
	blocker, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(t.Context()) }()
	if _, err := blocker.Exec(t.Context(), `SELECT id FROM apps WHERE id=$1 FOR UPDATE`, app.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.AdvanceRuntimeUpgradeVerification(t.Context(), claim); done <- err }()
	// Prove the worker actually reached the app lock before its lease expired.
	waitUntil := time.Now().Add(time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, int32(blocker.Conn().PgConn().PID())).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(waitUntil) {
			t.Fatal("verification never reached app fence")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(2100 * time.Millisecond)
	if err := blocker.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, state.ErrConflict) {
		t.Fatal("expired worker checkpointed", err)
	}
	j, err := s.RuntimeUpgradeVerificationJournal(t.Context(), r.AccountID, r.ID)
	if err != nil || j.Phase != state.RuntimeUpgradeVerificationPending || j.LastObservation != nil || j.LeaseToken != claim.LeaseToken {
		t.Fatal("expired advance published progress", j, err)
	}
	if did, err := (runtimeupgrade.VerificationExecutor{Store: state.NewPgStore(pool)}).RunOnce(t.Context()); err != nil || !did {
		t.Fatal("fresh worker could not resume", did, err)
	}
}
