package state_test

// adr: 457

import (
	"encoding/json"
	"errors"
	"io/fs"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
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

func healthNotificationHook(t *testing.T, s state.Store, accountID, appID, name string, events []string, enabled bool) state.AppWebhook {
	t.Helper()
	hook, err := s.CreateAppWebhook(t.Context(), state.AppWebhook{ID: uuid.NewString(), AccountID: accountID, AppID: appID, TargetURL: "https://example.test/" + name, SecretSealed: []byte("sealed"), Enabled: enabled, EventFilter: events})
	if err != nil {
		t.Fatal(err)
	}
	return hook
}

func healthNotificationPending(t *testing.T, s state.Store, want int64) {
	t.Helper()
	health, err := s.(state.AppWebhookEventOutboxHealthStore).AppWebhookEventOutboxHealth(t.Context())
	if err != nil || health.PendingCount != want {
		t.Fatalf("pending %+v %v want %d", health, err, want)
	}
}

func TestRouteHealthNotificationsMemActualHolds(t *testing.T) {
	s := state.NewMemStore()
	a, app, _, d := healthFixture(t, s)
	hook := healthNotificationHook(t, s, a.ID, app.ID, "health", []string{"routes.health.blocked", "routes.health.resumed"}, true)
	zero := int64(0)
	g, err := s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "enforce", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{{Method: "POST", Path: "/checkout", MaxP95MS: 300}}})
	if err != nil {
		t.Fatal(err)
	}
	var decision api.RouteHealthDecision
	params := state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 10, RouteHealthDecision: &decision}
	for range 3 {
		if _, _, err := s.AdvanceCanary(t.Context(), d.ID, params); err == nil {
			t.Fatal("unknown advanced")
		}
	}
	healthNotificationPending(t, s, 1)
	late := healthNotificationHook(t, s, a.ID, app.ID, "late", []string{"routes.health.blocked"}, true)
	if n, err := s.DrainAppWebhookEventOutbox(t.Context(), 32); err != nil || n != 1 {
		t.Fatal("hold relay", n, err)
	}
	rows, _, err := s.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 10, "")
	if err != nil || len(rows) != 1 {
		t.Fatal("hold delivery", err)
	}
	var payload api.RouteHealthTransitionWebhookPayload
	if json.Unmarshal(rows[0].Payload, &payload) != nil || payload.HealthStatus != "unknown" || payload.DecisionID != decision.HistoryID || payload.BlockedDecisionID != "" || strings.Contains(string(rows[0].Payload), "/checkout") {
		t.Fatal("hold evidence/privacy", string(rows[0].Payload))
	}
	if rows, _, _ := s.ListAppWebhookDeliveries(t.Context(), app.ID, late.ID, 10, ""); len(rows) != 0 {
		t.Fatal("late recipient received historical hold")
	}
	if _, err := s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &g.Revision, Routes: g.Routes}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AdvanceCanary(t.Context(), d.ID, params); err != nil {
		t.Fatal(err)
	}
	healthNotificationPending(t, s, 0)
}

func healthNotificationPG(t *testing.T) (*pgxpool.Pool, *state.PgStore, state.Account, state.App, state.Deployment, func(int32, int32)) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	a, app, stable, d := healthFixture(t, s)
	if _, err := pool.Exec(t.Context(), "UPDATE deployments SET canary_step_started_at=clock_timestamp()-interval '1 hour',rollout_state='rolling_out' WHERE id=$1", d.ID); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	if _, err := s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "enforce", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{{Method: "POST", Path: "/checkout", MaxP95MS: 300}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE route_health_gates SET updated_at=clock_timestamp()-interval '1 hour' WHERE app_id=$1", app.ID); err != nil {
		t.Fatal(err)
	}
	seed := func(count, latency int32) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), "DELETE FROM request_telemetry WHERE app_id=$1", app.ID); err != nil {
			t.Fatal(err)
		}
		for _, window := range routehealth.Windows(time.Now().UTC()) {
			for _, dep := range []state.Deployment{stable, d} {
				n, ms := int32(100), int32(100)
				if dep.ID == d.ID {
					n, ms = count, latency
				}
				if err := sqlc.New().InsertRequestTelemetry(t.Context(), pool, sqlc.InsertRequestTelemetryParams{AccountID: pgtype.UUID{Bytes: uuid.MustParse(a.ID), Valid: true}, AppID: pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true}, DeploymentID: pgtype.UUID{Bytes: uuid.MustParse(dep.ID), Valid: true}, Route: "POST /checkout", Method: "POST", Status: 200, LatencyMs: ms, Count: n, ReceivedAt: state.NewPgtypeTime(window.Start.Add(time.Second)), UaFamily: "__unknown__", ReferrerHost: "__none__", Country: "__unknown__", GuestRuntime: "__unknown__", GuestOutcome: "missing", FlagEvidenceJson: "[]"}); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	return pool, s, a, app, d, seed
}

func TestRouteHealthNotificationsPostgresTransitionsRecipientsAndRestart(t *testing.T) {
	pool, s, a, app, d, seed := healthNotificationPG(t)
	events := []string{"routes.health.blocked", "routes.health.resumed"}
	hook := healthNotificationHook(t, s, a.ID, app.ID, "health", events, true)
	wildcard := healthNotificationHook(t, s, a.ID, app.ID, "all", nil, true)
	disabled := healthNotificationHook(t, s, a.ID, app.ID, "disabled", events, false)
	filtered := healthNotificationHook(t, s, a.ID, app.ID, "filtered", []string{"routes.requirements.changed"}, true)
	other, err := s.CreateAccount(t.Context(), uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreign := healthNotificationHook(t, s, other.ID, app.ID, "foreign", events, true)
	accountHook, err := s.CreateAccountReleaseWebhookIfUnderQuota(t.Context(), state.AppWebhook{AccountID: a.ID, Scope: state.AppWebhookScopeAccount, TargetURL: "https://example.test/account", SecretSealed: []byte("sealed"), Enabled: true, EventFilter: []string{"deployment.live"}}, api.Limits{WebhookPerAccount: 20})
	if err != nil {
		t.Fatal(err)
	}
	// Account receivers cannot select app-only health events, even through SQL.
	if _, err := pool.Exec(t.Context(), "UPDATE app_webhooks SET event_filter=ARRAY['routes.health.blocked','routes.health.resumed'] WHERE id=$1", accountHook.ID); err == nil {
		t.Fatal("account receiver accepted app-only health events")
	}
	seed(99, 500)
	var decision api.RouteHealthDecision
	advance := func() error {
		_, _, err := s.AdvanceCanary(t.Context(), d.ID, state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 10, RouteHealthDecision: &decision, Audit: state.DeploymentAudit{Kind: state.DeployTrafficChanged, Actor: "notification-test"}})
		return err
	}
	if err := advance(); err == nil {
		t.Fatal("sparse evidence advanced")
	}
	unknownID := decision.HistoryID
	healthNotificationPending(t, s, 1)
	late := healthNotificationHook(t, s, a.ID, app.ID, "late", events, true)
	restarted := state.NewPgStore(pool)
	if n, err := restarted.DrainAppWebhookEventOutbox(t.Context(), 32); err != nil || n != 1 {
		t.Fatal("restart lost hold", n, err)
	}
	for _, excluded := range []state.AppWebhook{disabled, filtered, foreign, late, accountHook} {
		rows, _, err := s.ListAppWebhookDeliveries(t.Context(), app.ID, excluded.ID, 10, "")
		if err != nil || len(rows) != 0 {
			t.Fatal("wrong hold recipient", excluded.ID, err)
		}
	}
	for _, recipient := range []state.AppWebhook{hook, wildcard} {
		rows, _, err := s.ListAppWebhookDeliveries(t.Context(), app.ID, recipient.ID, 10, "")
		if err != nil || len(rows) != 1 {
			t.Fatal("missing hold", err)
		}
		var payload api.RouteHealthTransitionWebhookPayload
		if json.Unmarshal(rows[0].Payload, &payload) != nil || payload.DecisionID != unknownID || payload.HealthStatus != "unknown" || payload.Version != 1 || strings.Contains(string(rows[0].Payload), "/checkout") {
			t.Fatal("hold payload", string(rows[0].Payload))
		}
	}
	// Exact and changed observation retries never reopen an existing hold.
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Go(func() {
			_, _, err := restarted.AdvanceCanary(t.Context(), d.ID, state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 10})
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		var blocked *state.RouteHealthBlockedError
		if !errors.As(err, &blocked) {
			t.Fatal(err)
		}
	}
	healthNotificationPending(t, s, 0)
	seed(100, 500)
	if err := advance(); err == nil {
		t.Fatal("regression advanced")
	}
	regressedID := decision.HistoryID
	healthNotificationPending(t, s, 1)
	if n, err := s.DrainAppWebhookEventOutbox(t.Context(), 32); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	seed(99, 500)
	_ = advance()
	healthNotificationPending(t, s, 0)
	seed(101, 500)
	_ = advance()
	healthNotificationPending(t, s, 0)
	seed(100, 120)
	if err := advance(); err != nil {
		t.Fatal("healthy resume", err)
	}
	healthyID := decision.HistoryID
	healthNotificationPending(t, s, 1)
	if n, err := restarted.DrainAppWebhookEventOutbox(t.Context(), 32); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	rows, _, err := s.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 10, "")
	if err != nil || len(rows) != 3 {
		t.Fatal("expected hold/escalation/resume", len(rows), err)
	}
	resumed := false
	for _, row := range rows {
		if row.Event != state.AppWebhookEventRouteHealthResumed {
			continue
		}
		var p api.RouteHealthTransitionWebhookPayload
		if json.Unmarshal(row.Payload, &p) != nil || p.DecisionID != healthyID || p.BlockedDecisionID != regressedID || p.HealthStatus != "healthy" || p.Status != "resumed" || p.PreviousTrafficPercent != 1 || p.RequestedTrafficPercent != 10 {
			t.Fatal("resume provenance", string(row.Payload))
		}
		resumed = true
	}
	if !resumed {
		t.Fatal("no committed resume")
	}
}

func TestRouteHealthNotificationsPostgresRollback(t *testing.T) {
	for _, fault := range []string{"outbox", "traffic", "audit", "lease"} {
		t.Run(fault, func(t *testing.T) {
			pool, s, a, app, d, seed := healthNotificationPG(t)
			healthNotificationHook(t, s, a.ID, app.ID, "rollback", []string{"routes.health.blocked", "routes.health.resumed"}, true)
			seed(100, 500)
			var decision api.RouteHealthDecision
			params := state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 10, RouteHealthDecision: &decision, Audit: state.DeploymentAudit{Kind: state.DeployTrafficChanged, Actor: "notification-test"}}
			if _, _, err := s.AdvanceCanary(t.Context(), d.ID, params); err == nil {
				t.Fatal("regression advanced")
			}
			if n, err := s.DrainAppWebhookEventOutbox(t.Context(), 32); err != nil || n != 1 {
				t.Fatal(n, err)
			}
			seed(100, 120)
			switch fault {
			case "outbox":
				_, err := pool.Exec(t.Context(), `CREATE FUNCTION reject_health_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced outbox failure'; END; $$; CREATE TRIGGER reject_health_event BEFORE INSERT ON app_webhook_event_outbox FOR EACH ROW EXECUTE FUNCTION reject_health_event()`)
				if err != nil {
					t.Fatal(err)
				}
			case "traffic":
				_, err := pool.Exec(t.Context(), `CREATE FUNCTION reject_health_traffic() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced traffic failure'; END; $$; CREATE TRIGGER reject_health_traffic BEFORE UPDATE OF traffic_percent ON deployments FOR EACH ROW EXECUTE FUNCTION reject_health_traffic()`)
				if err != nil {
					t.Fatal(err)
				}
			case "audit":
				params.Audit.Data = json.RawMessage(`{`)
			case "lease":
				if err := s.StampSafeReleaseWorkerLease(t.Context(), time.Second); err != nil {
					t.Fatal(err)
				}
				params.RequireSafeReleaseLease = true
				_, err := pool.Exec(t.Context(), `CREATE FUNCTION delay_health_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(1.2); RETURN NEW; END; $$; CREATE TRIGGER delay_health_event BEFORE INSERT ON app_webhook_event_outbox FOR EACH ROW EXECUTE FUNCTION delay_health_event()`)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := s.AdvanceCanary(t.Context(), d.ID, params); err == nil {
				t.Fatal("forced failure committed")
			}
			healthNotificationPending(t, s, 0)
			page, err := s.ListRouteHealthHistory(t.Context(), a.ID, app.ID, d.ID, 5, "")
			if err != nil || len(page.Entries) != 1 {
				t.Fatal("failed resume left evidence", err)
			}
			baseline, err := sqlc.New().ReadRouteHealthNotificationState(t.Context(), pool, sqlc.ReadRouteHealthNotificationStateParams{DeploymentID: d.ID, AppID: app.ID, AccountID: a.ID})
			if err != nil || baseline.Status != "blocked_regressed" {
				t.Fatal("failed resume cleared hold", baseline, err)
			}
			current, _ := s.DeploymentByID(t.Context(), d.ID)
			audits, _ := s.ListDeploymentAudit(t.Context(), d.ID, 10)
			if current.CanaryStep != 0 || current.TrafficPercent != 1 || len(audits) != 0 {
				t.Fatal("failure changed traffic/audit")
			}
			switch fault {
			case "outbox":
				_, err = pool.Exec(t.Context(), "DROP TRIGGER reject_health_event ON app_webhook_event_outbox")
			case "traffic":
				_, err = pool.Exec(t.Context(), "DROP TRIGGER reject_health_traffic ON deployments")
			case "lease":
				_, err = pool.Exec(t.Context(), "DROP TRIGGER delay_health_event ON app_webhook_event_outbox")
			}
			if err != nil {
				t.Fatal(err)
			}
			params.Audit.Data = nil
			params.RequireSafeReleaseLease = false
			// The lease case intentionally waits long enough to cross a route-health
			// window boundary. Refresh telemetry so the retry always sees settled evidence.
			seed(100, 120)
			if _, _, err := state.NewPgStore(pool).AdvanceCanary(t.Context(), d.ID, params); err != nil {
				t.Fatal("retry failed", err)
			}
			healthNotificationPending(t, s, 1)
		})
	}
}

func TestRouteHealthNotificationsPostgresLateSubscriberAndPruning(t *testing.T) {
	pool, s, a, app, d, seed := healthNotificationPG(t)
	seed(100, 500)
	params := state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 10}
	if _, _, err := s.AdvanceCanary(t.Context(), d.ID, params); err == nil {
		t.Fatal("regression advanced")
	}
	healthNotificationPending(t, s, 0)
	hook := healthNotificationHook(t, s, a.ID, app.ID, "late", []string{"routes.health.blocked", "routes.health.resumed"}, true)
	for i := range api.RouteHealthHistoryMaxEntries + 1 {
		seed(int32(101+i), 500)
		if _, _, err := s.AdvanceCanary(t.Context(), d.ID, params); err == nil {
			t.Fatal("regression advanced")
		}
	}
	healthNotificationPending(t, s, 0)
	row, err := sqlc.New().ReadRouteHealthNotificationState(t.Context(), pool, sqlc.ReadRouteHealthNotificationStateParams{DeploymentID: d.ID, AppID: app.ID, AccountID: a.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetRouteHealthHistoryEntry(t.Context(), a.ID, app.ID, d.ID, row.BlockedDecisionID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("fixture did not prune original hold", err)
	}
	seed(100, 120)
	if _, _, err := s.AdvanceCanary(t.Context(), d.ID, params); err != nil {
		t.Fatal(err)
	}
	if n, err := s.DrainAppWebhookEventOutbox(t.Context(), 32); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	rows, _, err := s.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 10, "")
	if err != nil || len(rows) != 1 || rows[0].Event != state.AppWebhookEventRouteHealthResumed {
		t.Fatal("late recipient or pruning changed baseline", err)
	}
}

func TestRouteHealthNotificationsMigrationUpgrade(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	paths, err := fs.Glob(migrations.FS, "*_route_health_notifications.sql")
	if err != nil || len(paths) != 1 {
		t.Fatal("missing notification migration", err)
	}
	version, err := strconv.ParseInt(strings.SplitN(paths[0], "_", 2)[0], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	// Restore the previous release's ledger/schema, then exercise the ordinary
	// upgrade path. The additive version must follow all existing migrations.
	if _, err := pool.Exec(t.Context(), "DROP TABLE route_health_notification_state"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "DELETE FROM goose_db_version WHERE version_id=$1", version); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal("upgrade failed", err)
	}
	var exists bool
	if err := pool.QueryRow(t.Context(), "SELECT to_regclass('route_health_notification_state') IS NOT NULL").Scan(&exists); err != nil || !exists {
		t.Fatal("upgrade missing notification state", err)
	}
}
