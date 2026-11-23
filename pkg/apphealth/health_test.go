package apphealth

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/state"
)

func healthEvidence() Evidence {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	live := state.Deployment{ID: "serving", AppID: "app", Status: state.DeployLive, Scope: "default", TrafficPercent: 100, RootfsPath: "/artifact"}
	return Evidence{
		App: state.App{ID: "app", Status: state.AppActive}, Live: []state.Deployment{live}, Latest: &live,
		DeploymentsKnown: true, HistoryComplete: true, InstancesKnown: true, Now: now,
		Instances:      []state.Instance{{ID: "vm", DeploymentID: live.ID, NodeID: "node", State: string(state.StateRunning)}},
		Nodes:          map[string]state.ComputeNode{"node": {ID: "node", Active: true, LastHeartbeatAt: now}},
		Readiness:      map[string]map[string]state.InstanceReadiness{},
		MetricsAllowed: true,
		Metrics:        appmetrics.RequestHealth{Source: appmetrics.SourcePrometheus, AsOf: now.Format(time.RFC3339Nano), RequestCount: 100},
	}
}

// Prod findings 2026-11-15 #43: every fresh boot of a crash-looping release
// failed while the old replica kept serving, and app health said "healthy".
func TestAppHealth_ReportsRecentBootFailures(t *testing.T) {
	check := func(e Evidence) (string, bool) {
		for _, c := range Evaluate(e).Checks {
			if c.Code == "readiness" && strings.Contains(c.Detail, "failed to boot or exited in the last") {
				return c.Status, true
			}
		}
		return "", false
	}
	e := healthEvidence()
	if _, found := check(e); found {
		t.Fatal("boot failures reported without failed instances")
	}
	failed := func(id, deployment string, ago time.Duration) state.Instance {
		return state.Instance{ID: id, DeploymentID: deployment, State: string(state.StateFailed), StartedAt: e.Now.Add(-ago)}
	}
	e.RecentInstances = []state.Instance{
		failed("old", "serving", BootFailureWindow+time.Minute),
		failed("other-release", "retired", time.Minute),
		{ID: "stopped", DeploymentID: "serving", State: string(state.StateStopped), StartedAt: e.Now.Add(-time.Minute)},
	}
	if _, found := check(e); found {
		t.Fatal("boot failures counted an old failure, a retired release or a stopped instance")
	}
	e.RecentInstances = append(e.RecentInstances, failed("crash-1", "serving", 2*time.Minute), failed("crash-2", "serving", time.Minute))
	status, found := check(e)
	if !found || (status != Warning && status != Fail) {
		t.Fatalf("readiness with boot failures = %q (found %t), want warning or fail", status, found)
	}
	if got := Evaluate(e).Status; got == Healthy {
		t.Fatalf("status = %q while new boots fail", got)
	}
}

// Prod hunt r33: a serving app crash-looped (guest-init exhausted its
// restarts), schedd stopped the instance with liveness_failed, and app
// health and inspect reported nothing, because the row ends STOPPED.
func TestAppHealth_ReportsInstancesThatExited(t *testing.T) {
	e := healthEvidence()
	e.RecentInstances = []state.Instance{
		{ID: "crashed", DeploymentID: "serving", State: string(state.StateStopped), StartedAt: e.Now.Add(-time.Hour)},
		{ID: "parked-normally", DeploymentID: "serving", State: string(state.StateStopped), StartedAt: e.Now.Add(-time.Minute)},
	}
	e.ExitedInstances = map[string]bool{"crashed": true}
	found := false
	for _, c := range Evaluate(e).Checks {
		if c.Code == "readiness" && strings.Contains(c.Detail, "1 instance(s) of a serving release failed to boot or exited") {
			found = c.Status == Warning || c.Status == Fail
		}
	}
	if !found {
		t.Fatalf("exited instance not reported: %+v", Evaluate(e).Checks)
	}
}

type exitedEvidenceStore struct {
	EvidenceStore
	events map[string][]state.Event
	asked  []string
}

func (s *exitedEvidenceStore) ListEventsBySubjectKind(_ context.Context, subject, kind string, _ time.Time, _ int) ([]state.Event, error) {
	s.asked = append(s.asked, subject)
	if kind != livenessFailedKind {
		return nil, nil
	}
	return s.events[subject], nil
}

// Only recent STOPPED non-worker instances are looked up, and only those
// with a liveness_failed transition are marked.
func TestCollectExitedInstances(t *testing.T) {
	now := time.Now().UTC()
	store := &exitedEvidenceStore{events: map[string][]state.Event{"crashed": {{Kind: livenessFailedKind, At: now}}}}
	e := &Evidence{RecentInstances: []state.Instance{
		{ID: "crashed", State: string(state.StateStopped), LastRequestAt: now.Add(-time.Minute)},
		{ID: "parked", State: string(state.StateStopped), LastRequestAt: now.Add(-time.Minute)},
		{ID: "old", State: string(state.StateStopped), LastRequestAt: now.Add(-BootFailureWindow - time.Minute)},
		{ID: "worker", State: string(state.StateStopped), Mode: string(state.InstanceModeWorker), LastRequestAt: now},
		{ID: "running", State: string(state.StateRunning), LastRequestAt: now},
	}}
	collectExitedInstances(t.Context(), store, e)
	if len(e.ExitedInstances) != 1 || !e.ExitedInstances["crashed"] {
		t.Fatalf("exited = %v", e.ExitedInstances)
	}
	if strings.Join(store.asked, ",") != "crashed,parked" {
		t.Fatalf("looked up %v, want only recent stopped non-workers", store.asked)
	}
}

func TestAppHealth_EvidenceMatrix(t *testing.T) {
	tests := []struct {
		name          string
		change        func(*Evidence)
		status, phase string
	}{
		{"healthy", func(*Evidence) {}, Healthy, "serving"},
		{"expected idle", func(e *Evidence) { e.Instances = nil }, Healthy, "idle"},
		{"idle without artifact", func(e *Evidence) { e.Instances = nil; e.Live[0].RootfsPath = "" }, Unknown, "idle"},
		{"no traffic", func(e *Evidence) { e.Metrics.RequestCount = 0 }, Healthy, "serving"},
		{"metrics disabled", func(e *Evidence) { e.Metrics.Source = "degraded: secret endpoint" }, Unknown, "serving"},
		{"metrics missing timestamp", func(e *Evidence) { e.Metrics.AsOf = "" }, Unknown, "serving"},
		{"metrics stale", func(e *Evidence) { e.Metrics.AsOf = e.Now.Add(-3 * time.Minute).Format(time.RFC3339) }, Unknown, "serving"},
		{"metrics future", func(e *Evidence) { e.Metrics.AsOf = e.Now.Add(3 * time.Minute).Format(time.RFC3339) }, Unknown, "serving"},
		{"restricted telemetry", func(e *Evidence) { e.MetricsAllowed = false }, Unknown, "serving"},
		{"recent server errors", func(e *Evidence) { e.Metrics.ErrorRatePct = 5; e.Metrics.ServerErrors = 5 }, Degraded, "serving"},
		{"failed latest older serving", func(e *Evidence) { e.Latest = &state.Deployment{ID: "failed", Status: state.DeployFailed} }, Degraded, "serving"},
		{"failed first release", func(e *Evidence) {
			e.Live = nil
			e.Latest = &state.Deployment{ID: "failed", Status: state.DeployFailed}
		}, Unhealthy, "not_deployed"},
		{"deploying with older serving", func(e *Evidence) { e.Latest = &state.Deployment{ID: "building", Status: state.DeployBuilding} }, Healthy, "deploying"},
		{"no deployment", func(e *Evidence) { e.Live = nil; e.Latest = nil }, Unknown, "not_deployed"},
		{"service no replicas", func(e *Evidence) { e.App.Manifest.ExecutionMode = "service"; e.Instances = nil }, Unhealthy, "serving"},
		{"service starting", func(e *Evidence) {
			e.App.Manifest.ExecutionMode = "service"
			e.Instances[0].State = string(state.StateWaking)
		}, Degraded, "serving"},
		{"partial required capacity", func(e *Evidence) { e.App.MinInstances = 2 }, Degraded, "serving"},
		{"stopped service", func(e *Evidence) {
			e.App.Manifest.ExecutionMode = "service"
			e.App.Manifest.ServiceReplicas = &state.ServiceReplicas{Desired: 0}
			e.Instances = nil
		}, Healthy, "stopped"},
		{"unavailable node", func(e *Evidence) {
			e.Nodes["node"] = state.ComputeNode{Lifecycle: state.NodeLifecycleUnavailable, LastHeartbeatAt: e.Now}
		}, Unhealthy, "serving"},
		{"stale node", func(e *Evidence) {
			e.Nodes["node"] = state.ComputeNode{Active: true, LastHeartbeatAt: e.Now.Add(-2 * time.Minute)}
		}, Unhealthy, "serving"},
		{"missing node", func(e *Evidence) { e.Nodes = nil }, Unknown, "serving"},
		{"draining still serves", func(e *Evidence) {
			e.Nodes["node"] = state.ComputeNode{Lifecycle: state.NodeLifecycleDraining, LastHeartbeatAt: e.Now}
		}, Healthy, "serving"},
		{"bounded scan exceeded", func(e *Evidence) { e.InstancesKnown = false }, Unknown, "serving"},
		{"failed readiness survives missing telemetry", func(e *Evidence) {
			e.Metrics.Source = "degraded"
			e.Nodes["node"] = state.ComputeNode{Lifecycle: state.NodeLifecycleUnavailable, LastHeartbeatAt: e.Now}
		}, Unhealthy, "serving"},
		{"probe failure survives missing node", func(e *Evidence) {
			e.Nodes = nil
			e.Live[0].OverrideReadinessProbe = json.RawMessage(`{"path":"/ready"}`)
			e.Readiness["vm"] = map[string]state.InstanceReadiness{"primary_app": {Ready: false, At: e.Now}}
		}, Unhealthy, "serving"},
		{"failed latest with unknown serving", func(e *Evidence) {
			e.Live = nil
			e.DeploymentsKnown = false
			e.Latest = &state.Deployment{ID: "failed", Status: state.DeployFailed}
		}, Degraded, "unknown"},
		{"mirror excluded", func(e *Evidence) { e.Instances[0].Mode = "mirror"; e.App.MinInstances = 1 }, Unhealthy, "serving"},
		{"task excluded", func(e *Evidence) { e.Instances[0].Kind = "app_task"; e.App.MinInstances = 1 }, Unhealthy, "serving"},
		{"preview excluded", func(e *Evidence) { e.Live[0].Scope = "staging" }, Unknown, "not_deployed"},
		{"dark deployment excluded", func(e *Evidence) { e.Live[0].TrafficPercent = 0 }, Unknown, "not_deployed"},
		{"service traffic shard has no ready replica", func(e *Evidence) {
			e.App.Manifest.ExecutionMode = "service"
			d := e.Live[0]
			d.ID = "canary"
			d.TrafficPercent = 10
			e.Live = append(e.Live, d)
		}, Degraded, "serving"},
		{"worker not released", func(e *Evidence) { e.App.Manifest.ExecutionMode = "worker" }, Unknown, "unsupported"},
		{"worker queue unread", func(e *Evidence) { e.App.Manifest.ExecutionMode = "worker"; e.AssessWorkers = true }, Unknown, "serving"},
		{"job not assessed", func(e *Evidence) { e.App.Manifest.ExecutionMode = "job"; e.AssessWorkers = true }, Unknown, "unsupported"},
		{"maintenance", func(e *Evidence) { e.App.MaintenanceMode = true }, Degraded, "maintenance"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := healthEvidence()
			tt.change(&e)
			out := Evaluate(e)
			if out.Status != tt.status || out.Phase != tt.phase {
				t.Fatalf("got %s/%s want %s/%s: %+v", out.Status, out.Phase, tt.status, tt.phase, out.Checks)
			}
			raw, _ := json.Marshal(out)
			if strings.Contains(string(raw), "secret endpoint") || strings.Contains(string(raw), "/artifact") {
				t.Fatalf("sensitive evidence leaked: %s", raw)
			}
		})
	}
}

func TestAppHealth_IndependentReadinessGates(t *testing.T) {
	for _, tt := range []struct {
		name               string
		primary, companion *bool
		want               string
	}{
		{"both ready", ptr(true), ptr(true), Healthy},
		{"primary unready companion ready", ptr(false), ptr(true), Unhealthy},
		{"companion unready primary ready", ptr(true), ptr(false), Unhealthy},
		{"missing primary", nil, ptr(true), Unknown},
		{"missing companion", ptr(true), nil, Unknown},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := healthEvidence()
			e.Live[0].OverrideReadinessProbe = json.RawMessage(`{"path":"/ready"}`)
			e.Live[0].Sidecars = json.RawMessage(`[{"name":"proxy","type":"sidecar","primary_ingress":true,"readiness_probe":{"http_get":{"path":"/ready"}}},{"name":"optional","type":"sidecar","readiness_probe":{"http_get":{"path":"/ready"}}}]`)
			signals := map[string]state.InstanceReadiness{"sidecar:optional": {Ready: false, At: e.Now}}
			// An old ready transition remains valid; transitions are not heartbeats.
			if tt.primary != nil {
				signals["primary_app"] = state.InstanceReadiness{Ready: *tt.primary, At: e.Now.Add(-24 * time.Hour)}
			}
			if tt.companion != nil {
				signals["sidecar:proxy"] = state.InstanceReadiness{Ready: *tt.companion, At: e.Now}
			}
			e.Readiness["vm"] = signals
			if out := Evaluate(e); out.Status != tt.want {
				t.Fatalf("%+v", out)
			}
		})
	}
}

func ptr(b bool) *bool { return &b }
