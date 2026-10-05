package apphealth

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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
		MetricsAllowed: true, MetricsSource: appmetrics.SourcePrometheus,
		Metrics: api.AppMetricsResponse{AsOf: now.Format(time.RFC3339Nano), RequestCount: 100},
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
		{"metrics disabled", func(e *Evidence) { e.MetricsSource = "degraded: secret endpoint" }, Unknown, "serving"},
		{"metrics missing timestamp", func(e *Evidence) { e.Metrics.AsOf = "" }, Unknown, "serving"},
		{"metrics stale", func(e *Evidence) { e.Metrics.AsOf = e.Now.Add(-3 * time.Minute).Format(time.RFC3339) }, Unknown, "serving"},
		{"metrics future", func(e *Evidence) { e.Metrics.AsOf = e.Now.Add(3 * time.Minute).Format(time.RFC3339) }, Unknown, "serving"},
		{"restricted telemetry", func(e *Evidence) { e.MetricsAllowed = false }, Unknown, "serving"},
		{"recent server errors", func(e *Evidence) { e.Metrics.ErrorRatePct = 2 }, Degraded, "serving"},
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
			e.MetricsSource = "degraded"
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
		{"worker not assessed", func(e *Evidence) { e.App.Manifest.ExecutionMode = "worker" }, Unknown, "unsupported"},
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
