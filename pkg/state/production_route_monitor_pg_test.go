package state_test

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/debugger"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/routemonitor"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func productionMonitorPG(t *testing.T) (*pgxpool.Pool, *state.PgStore, state.Account, state.App, state.Deployment, state.Deployment) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	a, app, stable, d := healthFixture(t, s)
	productionMonitorExec(t, pool, "UPDATE deployments SET traffic_percent=0 WHERE app_id=$1", app.ID)
	productionMonitorExec(t, pool, "UPDATE deployments SET traffic_percent=CASE WHEN id=$1 THEN 100 ELSE 0 END,canary_step=canary_total_steps,rollout_state='complete',created_at=clock_timestamp()-interval '2 hours',canary_step_started_at=clock_timestamp()-interval '1 hour',rollout_completed_at=clock_timestamp()-interval '1 hour',commit_sha=$3 WHERE app_id=$2", d.ID, app.ID, strings.Repeat("a", 40))
	budget := int64(500)
	_, err := s.SetRouteMonitor(t.Context(), a.ID, app.ID, api.SetRouteMonitorRequest{Enabled: true, ExpectedRevision: new(int64), Routes: []api.RouteMonitorRoute{{Method: "POST", Path: "/checkout", Max5xxRateBPS: &budget, MaxP95MS: 300}}})
	if err != nil {
		t.Fatal(err)
	}
	productionMonitorExec(t, pool, "UPDATE route_monitors SET updated_at=clock_timestamp()-interval '1 hour' WHERE app_id=$1", app.ID)
	return pool, s, a, app, stable, d
}
func productionMonitorExec(t *testing.T, pool *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}
func productionMonitorTraffic(t *testing.T, pool *pgxpool.Pool, a state.Account, app state.App, dep, method, path string, status, count, latency int32, spans bool) {
	t.Helper()
	q := sqlc.New()
	for _, w := range routehealth.Windows(time.Now()) {
		trace := strings.ReplaceAll(uuid.NewString(), "-", "")
		p := sqlc.InsertRequestTelemetryParams{AccountID: mustPgUUID(t, a.ID), AppID: mustPgUUID(t, app.ID), DeploymentID: mustPgUUID(t, dep), Route: method + " " + path, Method: method, Status: status, Count: count, LatencyMs: latency, ReceivedAt: state.NewPgtypeTime(w.Start.Add(time.Second)), TraceID: pgtype.Text{String: trace, Valid: true}, UaFamily: "__unknown__", ReferrerHost: "__none__", Country: "__unknown__", GuestRuntime: "node22", GuestOutcome: "ok", GuestDurationMs: latency / 2, FlagEvidenceJson: "[]"}
		if err := q.InsertRequestTelemetry(t.Context(), pool, p); err != nil {
			t.Fatal(err)
		}
		if spans {
			span := debugger.StoredSpan{TraceID: trace, SpanID: "db", Name: "secret db destination", DBStatement: "SELECT private_customer", StartTimeUnixNano: uint64(w.Start.UnixNano()), EndTimeUnixNano: uint64(w.Start.Add(200 * time.Millisecond).UnixNano()), DurationNanos: uint64(200 * time.Millisecond), Attributes: map[string]string{"gregale.dependency.type": "managed_binding", "gregale.dependency.kind": "postgres"}}
			body, _ := json.Marshal([]debugger.StoredSpan{span})
			if err := q.UpdateSpansSummary(t.Context(), pool, sqlc.UpdateSpansSummaryParams{TraceID: pgtype.Text{String: trace, Valid: true}, Column2: body, Column3: mustPgUUID(t, a.ID)}); err != nil {
				t.Fatal(err)
			}
		}
	}
}
func productionMonitorDue(t *testing.T, pool *pgxpool.Pool, appID string) {
	productionMonitorExec(t, pool, "UPDATE route_monitors SET next_check_at=clock_timestamp()-interval '1 second' WHERE app_id=$1", appID)
}
func productionMonitorEvaluate(t *testing.T, s *state.PgStore, a state.Account, app state.App) {
	t.Helper()
	done, err := s.EvaluateRouteMonitor(t.Context(), a.ID, app.ID)
	if err != nil || !done {
		t.Fatalf("evaluation done=%t: %v", done, err)
	}
}
func productionMonitorClear(t *testing.T, pool *pgxpool.Pool, appID string) {
	productionMonitorExec(t, pool, "DELETE FROM request_telemetry WHERE app_id=$1", appID)
}

// ADR-464: production evaluation survives promotion, snapshots diagnostics and
// emits exactly one open/recovery transition without inferring recovery from unknown.
func TestProductionRouteMonitorPostgresLifecycleAndSavedEvidence(t *testing.T) {
	pool, s, a, app, stable, d := productionMonitorPG(t)
	hook := healthNotificationHook(t, s, a.ID, app.ID, "production", []string{"routes.monitor.violated", "routes.monitor.recovered"}, true)
	productionMonitorTraffic(t, pool, a, app, d.ID, "POST", "/checkout", 200, 90, 100, true)
	productionMonitorTraffic(t, pool, a, app, d.ID, "POST", "/checkout", 500, 10, 600, true)
	productionMonitorTraffic(t, pool, a, app, stable.ID, "POST", "/checkout", 500, 999, 9999, false)
	productionMonitorTraffic(t, pool, a, app, d.ID, "GET", "/checkout", 500, 999, 9999, false)
	productionMonitorTraffic(t, pool, a, app, d.ID, "POST", "/checkout/123", 500, 999, 9999, false)
	report, err := s.GetRouteMonitorReport(t.Context(), a.ID, app.ID)
	if err != nil || report.Status != "violated" || report.DeploymentID != d.ID || routemonitor.ValidateReport(report) != nil {
		t.Fatalf("promoted report %+v %v", report, err)
	}
	if report.Routes[0].Windows[0].Observed.Requests != 100 || *report.Routes[0].Windows[0].Observed.P95LatencyMS != 600 {
		t.Fatal("scope or publisher weights lost")
	}
	page, err := s.ListRouteMonitorIncidents(t.Context(), a.ID, app.ID, 5, "")
	if err != nil || len(page.Incidents) != 0 {
		t.Fatal("read persisted an incident")
	}
	productionMonitorEvaluate(t, s, a, app)
	page, err = s.ListRouteMonitorIncidents(t.Context(), a.ID, app.ID, 5, "")
	if err != nil || len(page.Incidents) != 1 {
		t.Fatalf("%+v %v", page, err)
	}
	opened := page.Incidents[0]
	if opened.Status != "open" || len(opened.Evidence) != 2 {
		t.Fatalf("missing independent diagnostics %+v", opened)
	}
	if err := routemonitor.ValidateIncident(opened, app.Slug); err != nil {
		t.Fatal(err)
	}
	if opened.Evidence[0].Windows[0].Requests.MatchingRequests != 10 || opened.Evidence[1].Windows[0].Requests.MatchingRequests != 100 || *opened.Evidence[1].Windows[0].Diagnostics.Candidate.GuestP95MS != 300 || len(opened.Evidence[1].Windows[0].Diagnostics.Dependencies) != 1 {
		t.Fatal("saved evidence inventory lost")
	}
	body, _ := json.Marshal(opened)
	if strings.Contains(string(body), "secret db") || strings.Contains(string(body), "SELECT private_customer") {
		t.Fatal("private span data escaped")
	}
	if path := os.Getenv("ROUTE_MONITOR_FIXTURE_OUT"); path != "" {
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var event string
	var payload []byte
	var recipients []string
	if err := pool.QueryRow(t.Context(), "SELECT event,payload,recipient_webhook_ids::text[] FROM app_webhook_event_outbox WHERE app_id=$1", app.ID).Scan(&event, &payload, &recipients); err != nil {
		t.Fatal(err)
	}
	if event != "routes.monitor.violated" || !reflect.DeepEqual(recipients, []string{hook.ID}) || strings.Contains(string(payload), "checkout") {
		t.Fatal("notification recipients or metadata escaped")
	}
	productionMonitorDue(t, pool, app.ID)
	productionMonitorEvaluate(t, state.NewPgStore(pool), a, app)
	healthNotificationPending(t, s, 1)
	// Lost or pruned request evidence is unknown and cannot recover the incident.
	productionMonitorClear(t, pool, app.ID)
	productionMonitorDue(t, pool, app.ID)
	productionMonitorEvaluate(t, s, a, app)
	healthNotificationPending(t, s, 1)
	saved, err := s.GetRouteMonitorIncident(t.Context(), a.ID, app.ID, opened.ID)
	if err != nil || !reflect.DeepEqual(saved, opened) {
		t.Fatal("opening evidence changed with retained telemetry")
	}
	late := healthNotificationHook(t, s, a.ID, app.ID, "late-production", []string{"routes.monitor.recovered"}, true)
	productionMonitorTraffic(t, pool, a, app, d.ID, "POST", "/checkout", 200, 100, 100, false)
	productionMonitorDue(t, pool, app.ID)
	productionMonitorEvaluate(t, s, a, app)
	healthNotificationPending(t, s, 2)
	recovered, err := s.GetRouteMonitorIncident(t.Context(), a.ID, app.ID, opened.ID)
	if err != nil || recovered.Status != "recovered" || routemonitor.ValidateIncident(recovered, app.Slug) != nil || !reflect.DeepEqual(recovered.OpeningReport, opened.OpeningReport) || !reflect.DeepEqual(recovered.Evidence, opened.Evidence) {
		t.Fatalf("recovery lost immutable evidence %+v %v", recovered, err)
	}
	if err := pool.QueryRow(t.Context(), "SELECT recipient_webhook_ids::text[] FROM app_webhook_event_outbox WHERE app_id=$1 AND event='routes.monitor.recovered'", app.ID).Scan(&recipients); err != nil {
		t.Fatal(err)
	}
	if len(recipients) != 2 || !(recipients[0] == late.ID || recipients[1] == late.ID) {
		t.Fatal("late recipient did not receive future recovery")
	}
	productionMonitorDue(t, pool, app.ID)
	productionMonitorEvaluate(t, s, a, app)
	healthNotificationPending(t, s, 2)
	productionMonitorTraffic(t, pool, a, app, d.ID, "POST", "/checkout", 500, 100, 600, false)
	productionMonitorDue(t, pool, app.ID)
	productionMonitorEvaluate(t, s, a, app)
	healthNotificationPending(t, s, 3)
	page, err = s.ListRouteMonitorIncidents(t.Context(), a.ID, app.ID, 1, "")
	if err != nil || len(page.Incidents) != 1 || page.NextBefore == "" || page.Incidents[0].ID == opened.ID {
		t.Fatal("recurrence or cursor missing")
	}
	older, err := s.ListRouteMonitorIncidents(t.Context(), a.ID, app.ID, 1, page.NextBefore)
	if err != nil || len(older.Incidents) != 1 || older.Incidents[0].ID != opened.ID {
		t.Fatal("incident cursor did not preserve ordering")
	}
	c, _ := s.GetRouteMonitor(t.Context(), a.ID, app.ID)
	_, err = s.SetRouteMonitor(t.Context(), a.ID, app.ID, api.SetRouteMonitorRequest{Enabled: false, ExpectedRevision: &c.Revision, Routes: c.Routes})
	if err != nil {
		t.Fatal(err)
	}
	superseded, _ := s.GetRouteMonitorIncident(t.Context(), a.ID, app.ID, page.Incidents[0].ID)
	if superseded.Status != "superseded" || superseded.RecoveryReport != nil {
		t.Fatal("disabled intent falsely recovered")
	}
	healthNotificationPending(t, s, 3)
	if _, err := s.GetRouteMonitorIncident(t.Context(), uuid.NewString(), app.ID, opened.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("account scope leak")
	}
	other, err := s.CreateApp(t.Context(), state.App{AccountID: a.ID, Slug: "other-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetRouteMonitorIncident(t.Context(), a.ID, other.ID, opened.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("app scope leak")
	}
	if _, err := s.ListRouteMonitorIncidents(t.Context(), a.ID, other.ID, 5, opened.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign cursor leak")
	}
	dep, _ := s.DeploymentByID(t.Context(), d.ID)
	if dep.TrafficPercent != 100 || dep.CanaryStep != dep.CanaryTotalSteps {
		t.Fatal("advisory monitor mutated release")
	}
}
func TestProductionRouteMonitorPostgresAtomicRetriesAndContext(t *testing.T) {
	pool, s, a, app, _, d := productionMonitorPG(t)
	healthNotificationHook(t, s, a.ID, app.ID, "atomic-production", []string{"routes.monitor.violated", "routes.monitor.recovered"}, true)
	productionMonitorTraffic(t, pool, a, app, d.ID, "POST", "/checkout", 500, 100, 600, false)
	productionMonitorExec(t, pool, "CREATE FUNCTION reject_production_route_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected outbox failure'; END $$")
	productionMonitorExec(t, pool, "CREATE TRIGGER reject_production_route_event BEFORE INSERT ON app_webhook_event_outbox FOR EACH ROW EXECUTE FUNCTION reject_production_route_event()")
	if done, err := s.EvaluateRouteMonitor(t.Context(), a.ID, app.ID); err == nil || done {
		t.Fatal("outbox failure committed incident")
	}
	page, _ := s.ListRouteMonitorIncidents(t.Context(), a.ID, app.ID, 5, "")
	if len(page.Incidents) != 0 {
		t.Fatal("failed notification left saved evidence")
	}
	productionMonitorExec(t, pool, "DROP TRIGGER reject_production_route_event ON app_webhook_event_outbox")
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { _, _ = s.EvaluateRouteMonitor(t.Context(), a.ID, app.ID) })
	}
	wg.Wait()
	page, _ = s.ListRouteMonitorIncidents(t.Context(), a.ID, app.ID, 5, "")
	if len(page.Incidents) != 1 {
		t.Fatal("concurrent workers duplicated incident")
	}
	healthNotificationPending(t, s, 1)
	opened := page.Incidents[0]
	// An active split is unknown and retains the prior incident.
	replacement, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:replacement", TrafficPercent: 1, CanaryTotalSteps: 4, CanaryPreset: "balanced"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(t.Context(), replacement.ID); err != nil {
		t.Fatal(err)
	}
	productionMonitorDue(t, pool, app.ID)
	productionMonitorEvaluate(t, s, a, app)
	original, _ := s.GetRouteMonitorIncident(t.Context(), a.ID, app.ID, opened.ID)
	if original.Status != "open" {
		t.Fatal("ambiguous serving context cleared incident")
	}
	productionMonitorExec(t, pool, "UPDATE deployments SET traffic_percent=0 WHERE app_id=$1", app.ID)
	productionMonitorExec(t, pool, "UPDATE deployments SET traffic_percent=CASE WHEN id=$1 THEN 100 ELSE 0 END,canary_step=canary_total_steps,canary_step_started_at=clock_timestamp()-interval '1 hour',rollout_completed_at=clock_timestamp()-interval '1 hour',created_at=clock_timestamp()-interval '2 hours' WHERE app_id=$2", replacement.ID, app.ID)
	productionMonitorTraffic(t, pool, a, app, replacement.ID, "POST", "/checkout", 500, 100, 600, false)
	productionMonitorDue(t, pool, app.ID)
	productionMonitorEvaluate(t, s, a, app)
	healthNotificationPending(t, s, 2)
	original, _ = s.GetRouteMonitorIncident(t.Context(), a.ID, app.ID, opened.ID)
	if original.Status != "superseded" || original.RecoveryReport != nil {
		t.Fatal("replacement claimed recovery")
	}
	page, _ = s.ListRouteMonitorIncidents(t.Context(), a.ID, app.ID, 5, "")
	if len(page.Incidents) != 2 || page.Incidents[0].DeploymentID != replacement.ID {
		t.Fatal("new deployment evidence missing")
	}
	// Entitlement loss preserves the incident, but still permits disabling intent.
	productionMonitorExec(t, pool, "UPDATE accounts SET plan='free' WHERE id=$1", a.ID)
	productionMonitorDue(t, pool, app.ID)
	productionMonitorEvaluate(t, s, a, app)
	healthNotificationPending(t, s, 2)
	if _, err := s.GetRouteMonitorIncident(t.Context(), a.ID, app.ID, opened.ID); !errors.Is(err, state.ErrRouteInvestigationPlan) {
		t.Fatal("incident read bypassed current entitlement")
	}
	c, _ := s.GetRouteMonitor(t.Context(), a.ID, app.ID)
	if _, err := s.SetRouteMonitor(t.Context(), a.ID, app.ID, api.SetRouteMonitorRequest{Enabled: false, ExpectedRevision: &c.Revision, Routes: c.Routes}); err != nil {
		t.Fatal("downgrade prevented disable")
	}
}
func TestProductionRouteMonitorPostgresFreshAnchorsEvidenceCapsRetentionAndRetryFence(t *testing.T) {
	pool, s, a, app, _, d := productionMonitorPG(t)
	c, _ := s.GetRouteMonitor(t.Context(), a.ID, app.ID)
	budget := int64(500)
	routes := []api.RouteMonitorRoute{}
	for _, path := range []string{"/checkout", "/login", "/orders", "/pay"} {
		routes = append(routes, api.RouteMonitorRoute{Method: "POST", Path: path, Max5xxRateBPS: &budget})
	}
	c, err := s.SetRouteMonitor(t.Context(), a.ID, app.ID, api.SetRouteMonitorRequest{Enabled: true, ExpectedRevision: &c.Revision, Routes: routes})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range routes {
		productionMonitorTraffic(t, pool, a, app, d.ID, r.Method, r.Path, 500, 20, 600, false)
	}
	report, err := s.GetRouteMonitorReport(t.Context(), a.ID, app.ID)
	if err != nil || report.Status != "unknown" || report.Routes[0].Windows[0].ErrorReason != "observation_window_not_elapsed" {
		t.Fatal("fresh configuration reused older windows")
	}
	productionMonitorExec(t, pool, "UPDATE route_monitors SET updated_at=clock_timestamp()-interval '1 hour' WHERE app_id=$1", app.ID)
	targets, err := s.ListDueRouteMonitors(t.Context())
	if err != nil || len(targets) != 1 {
		t.Fatal(err)
	}
	productionMonitorEvaluate(t, s, a, app)
	page, _ := s.ListRouteMonitorIncidents(t.Context(), a.ID, app.ID, 5, "")
	if len(page.Incidents) != 1 {
		t.Fatal("missing incident")
	}
	active := page.Incidents[0]
	if len(active.Evidence) != api.RouteMonitorEvidenceRoutesLimit || !active.EvidenceTruncated || routemonitor.ValidateIncident(active, app.Slug) != nil {
		t.Fatal("saved diagnostic cap not disclosed")
	}
	var before, after time.Time
	if err := pool.QueryRow(t.Context(), "SELECT next_check_at FROM route_monitors WHERE app_id=$1", app.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := s.DeferRouteMonitor(t.Context(), targets[0]); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), "SELECT next_check_at FROM route_monitors WHERE app_id=$1", app.ID).Scan(&after); err != nil || !before.Equal(after) {
		t.Fatal("failed-attempt retry overwrote a successful schedule")
	}
	inserted := map[string]bool{}
	for range api.RouteMonitorHistoryMaxEntries + 10 {
		closed := active
		closed.ID = uuid.NewString()
		closed.Status = "superseded"
		at := closed.OpenedAt
		closed.ClosedAt = &at
		body, _ := json.Marshal(closed)
		inserted[closed.ID] = true
		productionMonitorExec(t, pool, "INSERT INTO route_monitor_incidents(id,app_id,account_id,deployment_id,revision,status,opened_at,closed_at,encoded_bytes,entry) VALUES($1,$2,$3,$4,$5,'superseded',$6,$6,$7,$8)", closed.ID, app.ID, a.ID, d.ID, closed.Revision, closed.OpenedAt, len(body), body)
	}
	productionMonitorDue(t, pool, app.ID)
	productionMonitorEvaluate(t, s, a, app)
	rows, err := pool.Query(t.Context(), "SELECT id::text FROM route_monitor_incidents WHERE app_id=$1", app.ID)
	if err != nil {
		t.Fatal(err)
	}
	retained := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		delete(inserted, id)
		retained++
	}
	rows.Close()
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	if retained != api.RouteMonitorHistoryMaxEntries+1 || len(inserted) != 10 {
		t.Fatal("closed history cap or active incident retention failed")
	}
	saved, err := s.GetRouteMonitorIncident(t.Context(), a.ID, app.ID, active.ID)
	if err != nil || saved.Status != "open" {
		t.Fatal("pruned active incident")
	}
	for pruned := range inserted {
		if _, err := s.ListRouteMonitorIncidents(t.Context(), a.ID, app.ID, 5, pruned); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("pruned cursor did not return not found")
		}
		break
	}
	// A stale configuration's failed attempt cannot delay new intent.
	productionMonitorDue(t, pool, app.ID)
	targets, _ = s.ListDueRouteMonitors(t.Context())
	c, _ = s.GetRouteMonitor(t.Context(), a.ID, app.ID)
	routes[0].MaxP95MS = 300
	if _, err := s.SetRouteMonitor(t.Context(), a.ID, app.ID, api.SetRouteMonitorRequest{Enabled: true, ExpectedRevision: &c.Revision, Routes: routes}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeferRouteMonitor(t.Context(), targets[0]); err != nil {
		t.Fatal(err)
	}
	due, err := s.ListDueRouteMonitors(t.Context())
	if err != nil || len(due) != 1 || due[0].Revision == targets[0].Revision {
		t.Fatal("stale failure delayed edited intent")
	}
}
