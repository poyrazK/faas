package main

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/routeprobe"
	"github.com/onebox-faas/faas/pkg/state"
)

type probeChallengeNotifier struct {
	noopNotifier
	mu       sync.Mutex
	payloads map[string]string // deployment -> token
}

func (n *probeChallengeNotifier) Notify(_ context.Context, channel, payload string) error {
	if channel != db.NotifyRouteProbeChallenge {
		return nil
	}
	var p struct {
		DeploymentID string    `json:"deployment_id"`
		Token        string    `json:"token"`
		ExpiresAt    time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.payloads == nil {
		n.payloads = map[string]string{}
	}
	n.payloads[p.DeploymentID] = p.Token
	return nil
}

type fakeProbeSender struct {
	mu       sync.Mutex
	notifier *probeChallengeNotifier
	outcomes map[string]routeprobe.Outcome // deployment -> outcome
	calls    int
	paths    map[string]bool
}

func (f *fakeProbeSender) Configured() bool { return true }

func (f *fakeProbeSender) Probe(_ context.Context, _ string, deploymentID, token, method, path string) (routeprobe.Outcome, error) {
	f.notifier.mu.Lock()
	issued := f.notifier.payloads[deploymentID]
	f.notifier.mu.Unlock()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.paths == nil {
		f.paths = map[string]bool{}
	}
	f.paths[method+" "+path] = true
	if token == "" || token != issued {
		return routeprobe.Unattributed, nil
	}
	return f.outcomes[deploymentID], nil
}

// sparseReportStore reports the probed route as unknown only for lack of
// requests; MemStore itself has no telemetry.
type sparseReportStore struct {
	*state.MemStore
	stableID string
}

func (s *sparseReportStore) GetRouteHealthReport(_ context.Context, _, _, deploymentID string) (api.RouteHealthReport, error) {
	windows := routehealth.Windows(time.Now())
	for i := range windows {
		windows[i].ErrorStatus, windows[i].ErrorReason, windows[i].Status, windows[i].Reason = "unknown", "insufficient_requests", "unknown", "insufficient_requests"
	}
	return api.RouteHealthReport{DeploymentID: deploymentID, StableDeploymentID: s.stableID, Status: "unknown", Routes: []api.RouteHealthFinding{
		{Method: "GET", Path: "/reports/{id}", Status: "unknown", Reason: "comparisons_incomplete_or_unsettled", Windows: windows},
		{Method: "GET", Path: "/busy", Status: "healthy", Reason: "comparisons_healthy"},
	}}, nil
}

// adr: 954
func TestRouteProbeWorkerProbesSparseSelectorsOncePerMinute(t *testing.T) {
	old := routeProbeChallengeDelay
	routeProbeChallengeDelay = 0
	t.Cleanup(func() { routeProbeChallengeDelay = old })
	e := setup(t, api.PlanPro)
	app, stable, canary := seedCanaryFixture(t, e, "probe-worker")
	zero := int64(0)
	if _, err := e.store.SetRouteHealthGate(context.Background(), e.acct.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{
		{Method: "GET", Path: "/reports/{id}", Probe: &api.RouteHealthProbe{Path: "/reports/7"}},
		{Method: "GET", Path: "/busy", Probe: &api.RouteHealthProbe{Path: "/busy"}},
	}}); err != nil {
		t.Fatal(err)
	}
	notifier := &probeChallengeNotifier{}
	sender := &fakeProbeSender{notifier: notifier, outcomes: map[string]routeprobe.Outcome{canary.ID: routeprobe.ServerError, stable.ID: routeprobe.Success}}
	e.s.store = &sparseReportStore{MemStore: e.store, stableID: stable.ID}
	e.s.notif = notifier
	e.s.WithRouteProbes(sender)

	if err := e.s.drainRouteProbes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.s.drainRouteProbes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := 2 * api.RouteHealthProbeRequestsPerMinute; sender.calls != want {
		t.Fatalf("probe calls = %d, want %d (one sparse route, two deployments, one round per minute)", sender.calls, want)
	}
	if !sender.paths["GET /reports/7"] || sender.paths["GET /busy"] {
		t.Fatalf("probed paths = %v, want only the sparse route's concrete path", sender.paths)
	}
	got := map[string]state.RouteProbeObservation{}
	for _, o := range e.store.RouteProbeObservationsForTest(app.ID) {
		got[o.DeploymentID] = o
	}
	n := int64(api.RouteHealthProbeRequestsPerMinute)
	if c := got[canary.ID]; c.Requests != n || c.ServerErrors != n || c.Path != "/reports/{id}" {
		t.Fatalf("candidate observation = %+v", c)
	}
	if s := got[stable.ID]; s.Requests != n || s.ServerErrors != 0 || s.Path != "/reports/{id}" {
		t.Fatalf("stable observation = %+v", s)
	}
}

// adr: 954
func TestRouteProbeWorkerDisabledWithoutSender(t *testing.T) {
	e := setup(t, api.PlanPro)
	if err := e.s.drainRouteProbes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.s.routeProbes != nil {
		t.Fatal("route probes enabled without configuration")
	}
}
