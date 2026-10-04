package state_test

// adr: 458

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func healthAbortPG(t *testing.T) (*pgxpool.Pool, *state.PgStore, state.Account, state.App, state.Deployment, func(int32, int32)) {
	t.Helper()
	pool, s, a, app, d, seed := healthNotificationPG(t)
	gate, err := s.GetRouteHealthGate(t.Context(), a.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: gate.Mode, OnRegression: "abort", ExpectedRevision: &gate.Revision, Routes: gate.Routes}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE route_health_gates SET updated_at=clock_timestamp()-interval '1 hour' WHERE app_id=$1", app.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.StampSafeReleaseWorkerLease(t.Context(), 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	return pool, s, a, app, d, seed
}
func healthAbortErrors(t *testing.T, pool *pgxpool.Pool, a state.Account, app state.App, d state.Deployment) {
	t.Helper()
	for _, window := range routehealth.Windows(time.Now().UTC()) {
		err := sqlc.New().InsertRequestTelemetry(t.Context(), pool, sqlc.InsertRequestTelemetryParams{AccountID: pgtype.UUID{Bytes: uuid.MustParse(a.ID), Valid: true}, AppID: pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true}, DeploymentID: pgtype.UUID{Bytes: uuid.MustParse(d.ID), Valid: true}, Route: "POST /checkout", Method: "POST", Status: 500, Count: 10, LatencyMs: 100, ReceivedAt: state.NewPgtypeTime(window.Start.Add(time.Second)), UaFamily: "__unknown__", ReferrerHost: "__none__", Country: "__unknown__", GuestRuntime: "__unknown__", GuestOutcome: "missing", FlagEvidenceJson: "[]"})
		if err != nil {
			t.Fatal(err)
		}
	}
}
func TestRouteHealthAutomaticRecoveryCommitsExactEvidenceAndDeduplicates(t *testing.T) {
	pool, s, a, app, d, seed := healthAbortPG(t)
	hook := healthNotificationHook(t, s, a.ID, app.ID, "abort", []string{"routes.health.blocked", "routes.health.aborted", "routes.health.resumed"}, true)
	seed(99, 100)
	unknown, err := s.RecoverCanaryRouteHealth(t.Context(), a.ID, app.ID, d.ID, 0)
	if err != nil || unknown.Aborted || unknown.Decision == nil || unknown.Decision.Status != "blocked" {
		t.Fatal("unknown authorized recovery", unknown, err)
	}
	page, err := s.ListRouteHealthHistory(t.Context(), a.ID, app.ID, d.ID, 5, "")
	if err != nil || len(page.Entries) != 0 {
		t.Fatal("periodic unknown created history", err)
	}
	var held api.RouteHealthDecision
	if _, _, err := s.AdvanceCanary(t.Context(), d.ID, state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 10, RouteHealthDecision: &held}); err == nil {
		t.Fatal("unknown advance succeeded")
	}
	if _, err := s.DrainAppWebhookEventOutbox(t.Context(), 32); err != nil {
		t.Fatal(err)
	}
	seed(100, 100)
	healthAbortErrors(t, pool, a, app, d)
	var wg sync.WaitGroup
	results := make(chan state.RouteHealthRecoveryResult, 4)
	failures := make(chan error, 4)
	for range 4 {
		wg.Go(func() {
			result, err := state.NewPgStore(pool).RecoverCanaryRouteHealth(t.Context(), a.ID, app.ID, d.ID, 0)
			results <- result
			failures <- err
		})
	}
	wg.Wait()
	close(results)
	close(failures)
	var winner state.RouteHealthRecoveryResult
	aborts := 0
	for result := range results {
		if result.Aborted {
			winner = result
			aborts++
		}
	}
	for err := range failures {
		if err != nil && !errors.Is(err, state.ErrCanaryStateInvalid) {
			t.Fatal(err)
		}
	}
	if aborts != 1 || winner.Deployment.TrafficPercent != 0 || winner.Deployment.RolloutState != "aborted" {
		t.Fatal("recovery did not serialize", aborts, winner)
	}
	entry, err := s.GetRouteHealthHistoryEntry(t.Context(), a.ID, app.ID, d.ID, winner.Decision.HistoryID)
	if err != nil || entry.Purpose != "abort" || entry.Source != "worker" || entry.Decision.Status != "aborted" || entry.TrafficPercent != 1 || entry.RequestedTrafficPercent != 0 || !routehealth.AbortEligible(entry.Report) {
		t.Fatal("abort evidence", entry, err)
	}
	stable, err := s.DeploymentByID(t.Context(), entry.Report.StableDeploymentID)
	if err != nil || stable.TrafficPercent != 100 {
		t.Fatal("exact predecessor not restored", stable, err)
	}
	audits, err := s.ListDeploymentAudit(t.Context(), d.ID, 10)
	if err != nil || len(audits) != 1 || audits[0].Kind != state.DeployRolledBack || audits[0].Actor != "meterd:route_health_recovery" || !strings.Contains(string(audits[0].Data), entry.ID) {
		t.Fatal("recovery audit", audits, err)
	}
	if _, err := state.NewPgStore(pool).RecoverCanaryRouteHealth(t.Context(), a.ID, app.ID, d.ID, 0); !errors.Is(err, state.ErrCanaryStateInvalid) {
		t.Fatal("restart reopened aborted candidate", err)
	}
	healthNotificationPending(t, s, 1)
	if _, err := s.DrainAppWebhookEventOutbox(t.Context(), 32); err != nil {
		t.Fatal(err)
	}
	rows, _, err := s.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 10, "")
	if err != nil || len(rows) != 2 {
		t.Fatal("abort delivery", err, len(rows))
	}
	found := false
	for _, row := range rows {
		if row.Event == state.AppWebhookEventRouteHealthResumed {
			t.Fatal("abort claimed recovery")
		}
		if row.Event != state.AppWebhookEventRouteHealthAborted {
			continue
		}
		var payload api.RouteHealthTransitionWebhookPayload
		if json.Unmarshal(row.Payload, &payload) != nil || payload.Status != "aborted" || payload.DecisionID != entry.ID || payload.BlockedDecisionID != held.HistoryID || payload.RequestedTrafficPercent != 0 || strings.Contains(string(row.Payload), "/checkout") {
			t.Fatal("abort payload", string(row.Payload))
		}
		found = true
	}
	if !found {
		t.Fatal("missing abort event")
	}
}

func TestRouteHealthAutomaticRecoveryNegativeEvidenceAndContext(t *testing.T) {
	for _, scenario := range []string{"hold", "report", "latency_only", "healthy", "mixed", "sparse", "fresh_policy", "stale_step", "missing_lease", "expired_lease", "foreign_account", "ambiguous", "scope", "newer_stable", "missing_stable", "fresh_stage", "downgraded"} {
		t.Run(scenario, func(t *testing.T) {
			pool, s, a, app, d, seed := healthAbortPG(t)
			seed(100, 100)
			healthAbortErrors(t, pool, a, app, d)
			account, step := a.ID, 0
			switch scenario {
			case "hold", "report", "fresh_policy":
				gate, _ := s.GetRouteHealthGate(t.Context(), a.ID, app.ID)
				if scenario == "hold" {
					gate.OnRegression = "hold"
				}
				if scenario == "report" {
					gate.Mode = "report"
				}
				if scenario == "fresh_policy" {
					gate.Routes[0].MaxP95MS = 301
				}
				if _, err := s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: gate.Mode, OnRegression: gate.OnRegression, ExpectedRevision: &gate.Revision, Routes: gate.Routes}); err != nil {
					t.Fatal(err)
				}
			case "latency_only":
				seed(100, 500)
			case "healthy":
				seed(100, 100)
			case "mixed":
				windows := routehealth.Windows(time.Now().UTC())
				if _, err := pool.Exec(t.Context(), "DELETE FROM request_telemetry WHERE deployment_id=$1 AND status=500 AND received_at<$2", d.ID, windows[0].End); err != nil {
					t.Fatal(err)
				}
			case "sparse":
				seed(10, 100)
			case "newer_stable":
				if _, err := pool.Exec(t.Context(), "UPDATE deployments SET created_at=$1 WHERE app_id=$2 AND id<>$3", d.CreatedAt.Add(time.Hour), app.ID, d.ID); err != nil {
					t.Fatal(err)
				}
			case "missing_stable":
				if _, err := pool.Exec(t.Context(), "UPDATE deployments SET traffic_percent=0 WHERE app_id=$1 AND id<>$2", app.ID, d.ID); err != nil {
					t.Fatal(err)
				}
			case "fresh_stage":
				if _, err := pool.Exec(t.Context(), "UPDATE deployments SET canary_step_started_at=clock_timestamp() WHERE id=$1", d.ID); err != nil {
					t.Fatal(err)
				}
			case "downgraded":
				if _, err := pool.Exec(t.Context(), "UPDATE accounts SET plan='free' WHERE id=$1", a.ID); err != nil {
					t.Fatal(err)
				}
			case "stale_step":
				step = 1
			case "missing_lease":
				if _, err := pool.Exec(t.Context(), "DELETE FROM safe_release_worker_lease"); err != nil {
					t.Fatal(err)
				}
			case "expired_lease":
				if _, err := pool.Exec(t.Context(), "UPDATE safe_release_worker_lease SET healthy_at=clock_timestamp()-interval '2 seconds', expires_at=clock_timestamp()-interval '1 second'"); err != nil {
					t.Fatal(err)
				}
			case "foreign_account":
				account = uuid.NewString()
			case "ambiguous", "scope":
				scope := "default"
				if scenario == "scope" {
					scope = "staging"
				}
				extra := state.Deployment{ID: uuid.NewString(), AppID: app.ID, Scope: scope, Kind: state.DeploymentKindImage, ImageDigest: "sha256:other"}
				if scenario == "ambiguous" {
					extra.CanaryPreset, extra.CanaryTotalSteps, extra.RolloutState, extra.TrafficPercent = "balanced", 4, "pending", 1
				}
				other, err := s.CreateDeployment(t.Context(), extra)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.MarkDeploymentLive(t.Context(), other.ID); err != nil {
					t.Fatal(err)
				}

			}
			result, err := s.RecoverCanaryRouteHealth(t.Context(), account, app.ID, d.ID, step)
			if scenario == "scope" {
				if err != nil || !result.Aborted {
					t.Fatal("other scope blocked recovery", result, err)
				}
				var staging int
				if err := pool.QueryRow(t.Context(), "SELECT traffic_percent FROM deployments WHERE app_id=$1 AND scope='staging' AND status='live'", app.ID).Scan(&staging); err != nil || staging != 100 {
					t.Fatal("other scope traffic changed", staging, err)
				}
				return
			}
			if result.Aborted {
				t.Fatal("negative evidence authorized recovery", scenario)
			}
			if (scenario == "stale_step" || scenario == "missing_lease" || scenario == "expired_lease" || scenario == "foreign_account" || scenario == "ambiguous" || scenario == "newer_stable" || scenario == "missing_stable") && err == nil {
				t.Fatal("guard absent", scenario)
			}
			current, _ := s.DeploymentByID(t.Context(), d.ID)
			if current.RolloutState == "aborted" {
				t.Fatal("failed guard aborted candidate")
			}
			page, err := s.ListRouteHealthHistory(t.Context(), a.ID, app.ID, d.ID, 5, "")
			if err != nil || len(page.Entries) != 0 {
				t.Fatal("negative check retained history", err)
			}
			healthNotificationPending(t, s, 0)
		})
	}
}

func TestRouteHealthAutomaticRecoveryMigrationReplayAndDefault(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	paths, err := fs.Glob(migrations.FS, "*_route_health_automatic_abort.sql")
	if err != nil || len(paths) != 1 {
		t.Fatal(paths, err)
	}
	version, err := strconv.ParseInt(strings.SplitN(paths[0], "_", 2)[0], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	a, app, _, _ := healthFixture(t, s)
	if _, err := s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "enforce", OnRegression: "abort", ExpectedRevision: new(int64), Routes: []api.RouteHealthRoute{{Method: "POST", Path: "/checkout"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "ALTER TABLE route_health_gates DROP COLUMN on_regression"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "DELETE FROM goose_db_version WHERE version_id=$1", version); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal("upgrade failed", err)
	}
	gate, err := s.GetRouteHealthGate(t.Context(), a.ID, app.ID)
	if err != nil || gate.OnRegression != "hold" || gate.Revision != 1 || len(gate.Routes) != 1 {
		t.Fatal("existing policy did not upgrade to hold", gate, err)
	}
	if _, err := pool.Exec(t.Context(), "DELETE FROM goose_db_version WHERE version_id=$1", version); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal("replay failed", err)
	}
}

func TestRouteHealthAutomaticRecoveryRollsBackEveryWrite(t *testing.T) {
	for _, fault := range []string{"history", "traffic", "audit", "outbox", "lease"} {
		t.Run(fault, func(t *testing.T) {
			pool, s, a, app, d, _ := healthAbortPG(t)
			healthNotificationHook(t, s, a.ID, app.ID, "atomic", []string{"routes.health.aborted"}, true)
			healthAbortRecoveryEvidence(t, pool, a, app, d)
			table, operation, body := "route_health_history", "INSERT", "RAISE EXCEPTION 'forced recovery failure';"
			switch fault {
			case "traffic":
				table, operation = "deployments", "UPDATE OF traffic_percent"
			case "audit":
				table = "deployment_audit"
			case "outbox":
				table = "app_webhook_event_outbox"
			case "lease":
				table, body = "app_webhook_event_outbox", "PERFORM pg_sleep(1.2); RETURN NEW;"
				if err := s.StampSafeReleaseWorkerLease(t.Context(), time.Second); err != nil {
					t.Fatal(err)
				}
			}
			// The table and trigger operation come only from the fixed test cases above.
			if _, err := pool.Exec(t.Context(), "CREATE FUNCTION fail_route_recovery() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN "+body+" END; $$; CREATE TRIGGER fail_route_recovery BEFORE "+operation+" ON "+table+" FOR EACH ROW EXECUTE FUNCTION fail_route_recovery()"); err != nil {
				t.Fatal(err)
			}
			listener, err := pool.Acquire(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Release()
			if _, err := listener.Exec(t.Context(), "LISTEN deployment_changed"); err != nil {
				t.Fatal(err)
			}
			result, err := s.RecoverCanaryRouteHealth(t.Context(), a.ID, app.ID, d.ID, 0)
			if err == nil || result.Aborted {
				t.Fatal("failed write committed recovery", result, err)
			}
			if fault == "lease" && !errors.Is(err, state.ErrSafeReleaseLeaseUnavailable) {
				t.Fatal("lease was not rechecked after writes", err)
			}
			current, err := s.DeploymentByID(t.Context(), d.ID)
			if err != nil || current.TrafficPercent != 1 || current.RolloutState != "rolling_out" {
				t.Fatal("partial candidate recovery", current, err)
			}
			var stableTraffic int
			if err := pool.QueryRow(t.Context(), "SELECT traffic_percent FROM deployments WHERE app_id=$1 AND id<>$2 AND status='live'", app.ID, d.ID).Scan(&stableTraffic); err != nil || stableTraffic != 99 {
				t.Fatal("partial stable recovery", stableTraffic, err)
			}
			page, err := s.ListRouteHealthHistory(t.Context(), a.ID, app.ID, d.ID, 5, "")
			if err != nil || len(page.Entries) != 0 {
				t.Fatal("failed recovery retained evidence", err)
			}
			audits, err := s.ListDeploymentAudit(t.Context(), d.ID, 10)
			if err != nil || len(audits) != 0 {
				t.Fatal("failed recovery retained audit", err)
			}
			_, err = sqlc.New().ReadRouteHealthNotificationState(t.Context(), pool, sqlc.ReadRouteHealthNotificationStateParams{AccountID: a.ID, AppID: app.ID, DeploymentID: d.ID})
			if !errors.Is(err, pgx.ErrNoRows) {
				t.Fatal("failed recovery retained notification baseline", err)
			}
			healthNotificationPending(t, s, 0)
			wait, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			notification, err := listener.Conn().WaitForNotification(wait)
			cancel()
			if !errors.Is(err, context.DeadlineExceeded) || notification != nil {
				t.Fatal("rollback emitted gateway notification", notification, err)
			}
			if _, err := pool.Exec(t.Context(), "DROP TRIGGER fail_route_recovery ON "+table); err != nil {
				t.Fatal(err)
			}
			if err := s.StampSafeReleaseWorkerLease(t.Context(), time.Minute); err != nil {
				t.Fatal(err)
			}
			result, err = state.NewPgStore(pool).RecoverCanaryRouteHealth(t.Context(), a.ID, app.ID, d.ID, 0)
			if err != nil || !result.Aborted {
				t.Fatal("restart retry failed", result, err)
			}
			healthNotificationPending(t, s, 1)
			wait, cancel = context.WithTimeout(t.Context(), time.Second)
			notification, err = listener.Conn().WaitForNotification(wait)
			cancel()
			if err != nil || notification == nil || !strings.Contains(notification.Payload, d.ID) {
				t.Fatal("committed recovery did not notify gateway", notification, err)
			}
		})
	}
}

// The lease fault deliberately waits past expiry. Seed one coherent set of
// observations, including the next closed minute, so crossing the ingestion
// boundary cannot turn the restart retry into an unrelated unknown-evidence
// decision. These samples are historical even before that minute is eligible.
func healthAbortRecoveryEvidence(t *testing.T, pool *pgxpool.Pool, a state.Account, app state.App, candidate state.Deployment) {
	t.Helper()
	var stableID string
	if err := pool.QueryRow(t.Context(), "SELECT id::text FROM deployments WHERE app_id=$1 AND id<>$2 AND status='live'", app.ID, candidate.ID).Scan(&stableID); err != nil {
		t.Fatal(err)
	}
	windows := routehealth.Windows(time.Now().UTC())
	end := windows[len(windows)-1].End
	windows = append(windows, api.RouteHealthWindowEvidence{Start: end, End: end.Add(api.RouteHealthWindow)})
	for _, window := range windows {
		for _, sample := range []struct {
			deploymentID string
			status       int32
			count        int32
		}{{stableID, 200, 100}, {candidate.ID, 200, 100}, {candidate.ID, 500, 10}} {
			if err := sqlc.New().InsertRequestTelemetry(t.Context(), pool, sqlc.InsertRequestTelemetryParams{
				AccountID: pgtype.UUID{Bytes: uuid.MustParse(a.ID), Valid: true}, AppID: pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true},
				DeploymentID: pgtype.UUID{Bytes: uuid.MustParse(sample.deploymentID), Valid: true}, Route: "POST /checkout", Method: "POST",
				Status: sample.status, Count: sample.count, LatencyMs: 100, ReceivedAt: state.NewPgtypeTime(window.Start.Add(time.Second)),
				UaFamily: "__unknown__", ReferrerHost: "__none__", Country: "__unknown__", GuestRuntime: "__unknown__", GuestOutcome: "missing", FlagEvidenceJson: "[]",
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestRouteHealthAutomaticRecoveryUsesPolicyAfterLockWait(t *testing.T) {
	pool, s, a, app, d, seed := healthAbortPG(t)
	seed(100, 100)
	healthAbortErrors(t, pool, a, app, d)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), "SELECT id FROM apps WHERE id=$1 FOR UPDATE", app.ID); err != nil {
		t.Fatal(err)
	}
	// Simulate a policy writer with the app lock while recovery waits for it.
	if _, err := tx.Exec(t.Context(), "UPDATE route_health_gates SET on_regression='hold', revision=revision+1, updated_at=clock_timestamp() WHERE app_id=$1", app.ID); err != nil {
		t.Fatal(err)
	}
	result := make(chan state.RouteHealthRecoveryResult, 1)
	failure := make(chan error, 1)
	go func() {
		r, err := s.RecoverCanaryRouteHealth(t.Context(), a.ID, app.ID, d.ID, 0)
		result <- r
		failure <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock')").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("recovery did not reach app lock")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r, err := <-result, <-failure; err != nil || r.Aborted {
		t.Fatal("old policy authorized recovery", r, err)
	}
	current, _ := s.DeploymentByID(t.Context(), d.ID)
	if current.TrafficPercent != 1 || current.RolloutState == "aborted" {
		t.Fatal("policy race changed traffic")
	}
}

func TestRouteHealthAutomaticRecoveryActionCASAndOmission(t *testing.T) {
	_, s, a, app, _, _ := healthAbortPG(t)
	gate, err := s.GetRouteHealthGate(t.Context(), a.ID, app.ID)
	if err != nil || gate.OnRegression != "abort" {
		t.Fatal(gate, err)
	}
	saved, err := s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: gate.Mode, Routes: gate.Routes, ExpectedRevision: &gate.Revision})
	if err != nil || saved.OnRegression != "hold" || saved.Revision != gate.Revision+1 || !saved.UpdatedAt.After(*gate.UpdatedAt) {
		t.Fatal("omitted action did not reset hold and observation anchor", saved, err)
	}
	if _, err := s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: gate.Mode, OnRegression: "abort", Routes: gate.Routes, ExpectedRevision: &gate.Revision}); !errors.Is(err, state.ErrRouteHealthRevision) {
		t.Fatal("stale action reenabled abort", err)
	}
	same, err := s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: saved.Mode, Routes: saved.Routes, ExpectedRevision: &saved.Revision})
	if err != nil || same.Revision != saved.Revision || !same.UpdatedAt.Equal(*saved.UpdatedAt) {
		t.Fatal("same policy reset observation anchor", same, err)
	}
}
