package state_test

// adr: 20261028110044

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// route_health_holds counts an in-flight canary the enforce guard holds, and
// an aborted one only within the window; route_monitor_incidents counts the
// incidents open at any time within the window.
func TestRouteAlertReadsPostgres(t *testing.T) {
	pool, s, a, app, d, seed := healthNotificationPG(t)
	ctx := t.Context()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	seed(1, 100)
	var held api.RouteHealthDecision
	if _, _, err := s.AdvanceCanary(ctx, d.ID, state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 10, RouteHealthDecision: &held}); err == nil {
		t.Fatal("sparse evidence advanced a canary held by an enforce gate")
	}
	now := time.Now().UTC()
	holds, total, err := s.RouteHealthHoldsSince(ctx, app.ID, now.Add(-time.Hour), api.RouteAlertNotes)
	if err != nil || total != 1 || len(holds) != 1 || holds[0].DeploymentID != d.ID || holds[0].Status != "blocked_unknown" || holds[0].Reason == "" {
		t.Fatalf("holds = %+v total %d err %v; want the held canary with its reason", holds, total, err)
	}
	exec("UPDATE deployments SET canary_step = canary_total_steps WHERE id = $1", d.ID)
	if _, total, err := s.RouteHealthHoldsSince(ctx, app.ID, now.Add(-time.Hour), 1); err != nil || total != 0 {
		t.Fatalf("a finished canary's stale hold counted: total %d err %v", total, err)
	}
	exec("UPDATE route_health_notification_state SET status = 'aborted', blocked_decision_id = NULL, updated_at = $2 WHERE deployment_id = $1", d.ID, now.Add(-30*time.Minute))
	if _, total, err := s.RouteHealthHoldsSince(ctx, app.ID, now.Add(-time.Hour), 1); err != nil || total != 1 {
		t.Fatalf("an abort within the window: total %d err %v, want 1", total, err)
	}
	if _, total, err := s.RouteHealthHoldsSince(ctx, app.ID, now.Add(-15*time.Minute), 1); err != nil || total != 0 {
		t.Fatalf("an abort before the window: total %d err %v, want 0", total, err)
	}

	incident := func(status string, opened time.Time, closed *time.Time) string {
		t.Helper()
		id := uuid.NewString()
		exec(`INSERT INTO route_monitor_incidents(id, app_id, account_id, deployment_id, revision, status, opened_at, closed_at, encoded_bytes, entry)
			SELECT $1::uuid, $2::uuid, $3::uuid, $4::uuid, 1, $5, $6, $7, octet_length(e::text), e
			FROM (SELECT jsonb_build_object('version', 1, 'id', $1::uuid::text, 'app_id', $2::uuid::text, 'deployment_id', $4::uuid::text, 'revision', 1, 'status', $5::text,
				'opening_report', jsonb_build_object('reason', 'error budget exceeded'),
				'evidence', jsonb_build_array(jsonb_build_object('method', 'POST', 'path', '/checkout', 'signal', 'error_rate'))) AS e) entry`,
			id, app.ID, a.ID, d.ID, status, opened, closed)
		return id
	}
	recentClose, oldClose := now.Add(-10*time.Minute), now.Add(-4*time.Hour)
	open := incident("open", now.Add(-3*time.Hour), nil)
	recovered := incident("recovered", now.Add(-2*time.Hour), &recentClose)
	incident("recovered", now.Add(-5*time.Hour), &oldClose)
	incidents, total, err := s.RouteMonitorIncidentsSince(ctx, app.ID, now.Add(-time.Hour), 1)
	if err != nil || total != 2 || len(incidents) != 1 {
		t.Fatalf("incidents = %+v total %d err %v; want the open and the recently recovered incident counted, one returned", incidents, total, err)
	}
	got := incidents[0]
	if got.ID != recovered || got.Status != "recovered" || got.Reason != "error budget exceeded" || got.Method != "POST" || got.Path != "/checkout" || got.Signal != "error_rate" {
		t.Fatalf("newest incident = %+v, want %s with its opening reason and first evidence", got, recovered)
	}
	if _, total, err := s.RouteMonitorIncidentsSince(ctx, app.ID, now.Add(-time.Minute), 5); err != nil || total != 1 {
		t.Fatalf("only %s is open in the last minute: total %d err %v", open, total, err)
	}
}

// Saving a route guard can warn about selectors no request matched: the read
// returns only the labels the app served within the lookback.
func TestObservedRouteLabelsPostgres(t *testing.T) {
	_, s, a, app, _, seed := healthNotificationPG(t)
	seed(10, 100)
	observed, err := s.ObservedRouteLabels(t.Context(), a.ID, app.ID, time.Now().Add(-7*24*time.Hour), []string{"POST /checkout", "GET /checkout", "POST /missing"})
	if err != nil || len(observed) != 1 || observed[0] != "POST /checkout" {
		t.Fatalf("observed = %q err %v, want only POST /checkout", observed, err)
	}
	if observed, err := s.ObservedRouteLabels(t.Context(), a.ID, app.ID, time.Now().Add(time.Hour), []string{"POST /checkout"}); err != nil || len(observed) != 0 {
		t.Fatalf("observed after the lookback = %q err %v", observed, err)
	}
}
