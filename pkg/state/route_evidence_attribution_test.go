package state_test

// adr: 20261028110044

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/routemonitor"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func attributionTraffic(t *testing.T, pool *pgxpool.Pool, a state.Account, app state.App, dep string, status, count, latency int32, cold, refused bool) {
	t.Helper()
	for _, w := range routehealth.Windows(time.Now().UTC()) {
		p := sqlc.InsertRequestTelemetryParams{AccountID: mustPgUUID(t, a.ID), AppID: mustPgUUID(t, app.ID), DeploymentID: mustPgUUID(t, dep), Route: "POST /checkout", Method: "POST", Status: status, Count: count, LatencyMs: latency, ColdBoot: cold, PlatformRefusal: refused, ReceivedAt: state.NewPgtypeTime(w.Start.Add(2 * time.Second)), UaFamily: "__unknown__", ReferrerHost: "__none__", Country: "__unknown__", GuestRuntime: "__unknown__", GuestOutcome: "missing", FlagEvidenceJson: "[]"}
		if err := sqlc.New().InsertRequestTelemetry(t.Context(), pool, p); err != nil {
			t.Fatal(err)
		}
	}
}

// A healthy candidate that cold boots more than stable, behind a gateway
// that refused some requests for platform reasons, is not held: the guard
// ranks warm latency and skips platform refusals, and its investigations
// reconcile with that evidence (amendment 3).
func TestCanaryGuardRanksWarmLatencyAndSkipsPlatformRefusals(t *testing.T) {
	pool, s, a, app, d, seed := healthNotificationPG(t)
	seed(120, 100)
	attributionTraffic(t, pool, a, app, d.ID, 200, 40, 2000, true, false)
	attributionTraffic(t, pool, a, app, d.ID, 503, 30, 2, false, true)
	r, err := s.GetRouteHealthReport(t.Context(), a.ID, app.ID, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	w := r.Routes[0].Windows[0]
	if r.Status != "healthy" || w.Candidate.Requests != 160 || w.Candidate.ServerErrors != 0 || w.Candidate.LatencyRequests == nil || *w.Candidate.LatencyRequests != 120 ||
		w.Stable.LatencyRequests == nil || *w.Stable.LatencyRequests != 100 || w.Candidate.P95LatencyMS == nil || *w.Candidate.P95LatencyMS != 100 {
		t.Fatalf("status %s (%s), candidate %+v, stable %+v", r.Status, r.Reason, w.Candidate, w.Stable)
	}
	for _, tc := range []struct {
		signal string
		want   int64
	}{{"errors", 0}, {"latency", 120}} {
		opts := api.RouteHealthInvestigationOptions{Method: "POST", Path: "/checkout", Signal: tc.signal}
		inv, err := s.GetRouteHealthInvestigation(t.Context(), a.ID, app.ID, d.ID, opts)
		if err != nil {
			t.Fatal(err)
		}
		if err := routehealth.ValidateInvestigation(inv, opts, app.Slug); err != nil {
			t.Fatalf("%s investigation: %v", tc.signal, err)
		}
		if got := inv.Windows[0].Candidate.MatchingRequests; got != tc.want {
			t.Fatalf("%s investigation matched %d requests, want %d", tc.signal, got, tc.want)
		}
	}
}

// The production monitor keeps end-to-end latency, cold boots included, but
// skips platform refusals; its incident evidence reconciles with both and
// joins each cold boot to its scheduler wake.
func TestProductionMonitorKeepsColdBootsAndSkipsPlatformRefusals(t *testing.T) {
	pool, s, a, app, _, d := productionMonitorPG(t)
	productionMonitorTraffic(t, pool, a, app, d.ID, "POST", "/checkout", 200, 100, 100, false)
	for _, w := range routehealth.Windows(time.Now().UTC()) {
		wake, instance := uuid.NewString(), uuid.NewString()
		at := w.Start.Add(2 * time.Second)
		body, _ := json.Marshal(map[string]string{"wake_id": wake, "app_id": app.ID, "instance_id": instance})
		if err := s.AppendEventAt(t.Context(), "schedd", "wake.boot_started", nil, body, at.Add(-time.Second)); err != nil {
			t.Fatal(err)
		}
		if err := s.AppendEventAt(t.Context(), "schedd", "wake.boot_completed", nil, body, at.Add(-time.Second).Add(250*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		p := sqlc.InsertRequestTelemetryParams{AccountID: mustPgUUID(t, a.ID), AppID: mustPgUUID(t, app.ID), DeploymentID: mustPgUUID(t, d.ID), Route: "POST /checkout", Method: "POST", Status: 200, Count: 40, LatencyMs: 2000, ColdBoot: true, WakeID: pgtype.Text{String: wake, Valid: true}, InstanceID: pgtype.Text{String: instance, Valid: true}, ReceivedAt: state.NewPgtypeTime(at), UaFamily: "__unknown__", ReferrerHost: "__none__", Country: "__unknown__", GuestRuntime: "__unknown__", GuestOutcome: "missing", FlagEvidenceJson: "[]"}
		if err := sqlc.New().InsertRequestTelemetry(t.Context(), pool, p); err != nil {
			t.Fatal(err)
		}
	}
	attributionTraffic(t, pool, a, app, d.ID, 503, 30, 2, false, true)
	r, err := s.GetRouteMonitorReport(t.Context(), a.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	f := r.Routes[0]
	o := f.Windows[0].Observed
	if f.ErrorStatus != "healthy" || f.LatencyStatus != "violated" || o.Requests != 140 || o.ServerErrors != 0 || o.LatencyRequests != nil || o.P95LatencyMS == nil || *o.P95LatencyMS != 2000 {
		t.Fatalf("errors %s, latency %s, observed %+v", f.ErrorStatus, f.LatencyStatus, o)
	}
	productionMonitorDue(t, pool, app.ID)
	productionMonitorEvaluate(t, s, a, app)
	page, err := s.ListRouteMonitorIncidents(t.Context(), a.ID, app.ID, 5, "")
	if err != nil || len(page.Incidents) != 1 {
		t.Fatalf("incidents %+v: %v", page.Incidents, err)
	}
	opened := page.Incidents[0]
	if err := routemonitor.ValidateIncident(opened, app.Slug); err != nil {
		t.Fatal(err)
	}
	if len(opened.Evidence) != 1 || opened.Evidence[0].Signal != "latency" || opened.Evidence[0].Windows[0].Requests.MatchingRequests != 140 {
		t.Fatalf("incident evidence %+v", opened.Evidence)
	}
	if diag := opened.Evidence[0].Windows[0].Diagnostics; diag == nil || diag.Candidate.ColdBootRequests != 40 || diag.Candidate.WakeSamples != 1 || diag.Candidate.WakeBootP95MS == nil || *diag.Candidate.WakeBootP95MS != 250 {
		t.Fatalf("cold boots or their wake left the monitor's diagnostics: %+v", diag)
	}
}
