package state_test

// adr: 20261028110044

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func flaggedTraffic(t *testing.T, pool *pgxpool.Pool, a state.Account, app state.App, dep string, status, count, latency int32, pinned, longLived bool) {
	t.Helper()
	for _, w := range routehealth.Windows(time.Now().UTC()) {
		p := sqlc.InsertRequestTelemetryParams{AccountID: mustPgUUID(t, a.ID), AppID: mustPgUUID(t, app.ID), DeploymentID: mustPgUUID(t, dep), Route: "POST /checkout", Method: "POST", Status: status, Count: count, LatencyMs: latency, DeploymentPinned: pinned, LongLived: longLived, ReceivedAt: state.NewPgtypeTime(w.Start.Add(3 * time.Second)), UaFamily: "__unknown__", ReferrerHost: "__none__", Country: "__unknown__", GuestRuntime: "__unknown__", GuestOutcome: "missing", FlagEvidenceJson: "[]"}
		if err := sqlc.New().InsertRequestTelemetry(t.Context(), pool, p); err != nil {
			t.Fatal(err)
		}
	}
}

// Requests that named their deployment never count, and a long-lived
// response counts as a request but not as latency, for the canary guard
// (amendment 5).
func TestCanaryGuardSkipsPinnedRequestsAndLongLivedLatency(t *testing.T) {
	pool, s, a, app, d, seed := healthNotificationPG(t)
	seed(120, 100)
	flaggedTraffic(t, pool, a, app, d.ID, 503, 50, 5, true, false)
	flaggedTraffic(t, pool, a, app, d.ID, 200, 30, 600000, false, true)
	r, err := s.GetRouteHealthReport(t.Context(), a.ID, app.ID, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	w := r.Routes[0].Windows[0]
	if r.Status != "healthy" || w.Candidate.Requests != 150 || w.Candidate.ServerErrors != 0 || w.Candidate.LatencyRequests == nil || *w.Candidate.LatencyRequests != 120 ||
		w.Candidate.P95LatencyMS == nil || *w.Candidate.P95LatencyMS != 100 {
		t.Fatalf("status %s (%s), candidate %+v", r.Status, r.Reason, w.Candidate)
	}
}

// The production monitor skips requests that named their deployment and
// leaves long-lived responses out of its latency, reporting how many it
// ranked (amendment 5).
func TestProductionMonitorSkipsPinnedRequestsAndLongLivedLatency(t *testing.T) {
	pool, s, a, app, _, d := productionMonitorPG(t)
	productionMonitorTraffic(t, pool, a, app, d.ID, "POST", "/checkout", 200, 100, 100, false)
	flaggedTraffic(t, pool, a, app, d.ID, 500, 40, 5, true, false)
	flaggedTraffic(t, pool, a, app, d.ID, 200, 30, 600000, false, true)
	r, err := s.GetRouteMonitorReport(t.Context(), a.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	f := r.Routes[0]
	o := f.Windows[0].Observed
	if f.ErrorStatus != "healthy" || f.LatencyStatus != "healthy" || o.Requests != 130 || o.ServerErrors != 0 ||
		o.LatencyRequests == nil || *o.LatencyRequests != 100 || o.P95LatencyMS == nil || *o.P95LatencyMS != 100 {
		t.Fatalf("errors %s, latency %s, observed %+v", f.ErrorStatus, f.LatencyStatus, o)
	}
}
