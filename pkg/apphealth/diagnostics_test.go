package apphealth

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestReadinessDiagnosticsRetainIndependentFailures(t *testing.T) {
	e := healthEvidence()
	e.Nodes = nil
	e.Live[0].OverrideReadinessProbe = json.RawMessage(`{"path":"/ready"}`)
	e.Live[0].Sidecars = json.RawMessage(`[{"name":"proxy","type":"sidecar","primary_ingress":true,"readiness_probe":{"http_get":{"path":"/ready"}}},{"name":"optional","type":"sidecar","readiness_probe":{"http_get":{"path":"/ready"}}}]`)
	transition := e.Now.Add(-24 * time.Hour)
	e.Readiness["vm"] = map[string]state.InstanceReadiness{
		"primary_app": {Ready: false, At: transition}, "sidecar:optional": {Ready: false, At: e.Now},
	}
	out := Evaluate(e)
	if out.Status != Unhealthy || out.Capacity.Unready != 1 || out.Capacity.Unknown != 0 {
		t.Fatalf("%+v", out)
	}
	found := map[string]api.AppHealthFinding{}
	for _, check := range out.Checks {
		for _, f := range check.Findings {
			if f.InstanceID != "vm" || f.DeploymentID != "serving" {
				t.Fatalf("missing target: %+v", f)
			}
			found[f.Source] = f
		}
	}
	if len(found) != 3 || found["node"].Reason != "node_evidence_missing" || found["sidecar:proxy"].Reason != "readiness_missing" || found["primary_app"].Reason != "required_probe_unready" || found["primary_app"].ObservedAt != transition.Format(time.RFC3339Nano) {
		t.Fatalf("%+v", found)
	}
}

func TestReadinessDiagnosticsBoundWithoutTruncatingCapacity(t *testing.T) {
	e := healthEvidence()
	e.Instances = nil
	e.Nodes["node"] = state.ComputeNode{Lifecycle: state.NodeLifecycleUnavailable, LastHeartbeatAt: e.Now}
	for n := api.AppHealthFindingLimit + 10; n >= 0; n-- {
		e.Instances = append(e.Instances, state.Instance{ID: fmt.Sprintf("vm-%03d", n), DeploymentID: "serving", NodeID: "node", State: string(state.StateRunning)})
	}
	out := Evaluate(e)
	if out.Capacity.Unready != len(e.Instances) || out.Status != Unhealthy {
		t.Fatalf("%+v", out)
	}
	for _, check := range out.Checks {
		if check.Code == "readiness" && (len(check.Findings) != api.AppHealthFindingLimit || !check.FindingsTruncated || check.Findings[0].InstanceID != "vm-000") {
			t.Fatalf("%+v", check)
		}
	}
}

func TestReadinessDiagnosticsKeepFailureAheadOfMissingEvidence(t *testing.T) {
	e := healthEvidence()
	e.Nodes = nil
	e.Instances = nil
	e.Live[0].OverrideReadinessProbe = json.RawMessage(`{"path":"/ready"}`)
	for n := 0; n <= api.AppHealthFindingLimit; n++ {
		e.Instances = append(e.Instances, state.Instance{ID: fmt.Sprintf("vm-%03d", n), DeploymentID: "serving", State: string(state.StateRunning)})
	}
	failed := e.Instances[len(e.Instances)-1].ID
	e.Readiness[failed] = map[string]state.InstanceReadiness{"primary_app": {Ready: false, At: e.Now}}
	out := Evaluate(e)
	if out.Status != Unhealthy {
		t.Fatalf("%+v", out)
	}
	for _, c := range out.Checks {
		if c.Code == "readiness" && (len(c.Findings) != api.AppHealthFindingLimit || c.Findings[0].InstanceID != failed || c.Findings[0].Status != Fail || !c.FindingsTruncated) {
			t.Fatalf("%+v", c)
		}
	}
}

func TestRequestHealthSeverity(t *testing.T) {
	for _, tt := range []struct {
		name             string
		requests, errors int64
		status, reason   string
	}{
		{"isolated high-volume error", 10000, 1, Healthy, "request_errors_below_threshold"},
		{"low-volume errors", 10, 10, Unknown, "request_volume_insufficient"},
		{"minimum error count", 100, 4, Healthy, "request_errors_below_threshold"},
		{"warning boundary", 100, 5, Degraded, "request_error_rate_elevated"},
		{"severe boundary", 100, 25, Unhealthy, "request_error_rate_severe"},
		{"minimum volume", 50, 5, Degraded, "request_error_rate_elevated"},
		{"low-volume success", 10, 0, Healthy, "requests_observed"},
		{"no traffic", 0, 0, Healthy, "requests_unexercised"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := healthEvidence()
			e.Metrics.RequestCount, e.Metrics.ServerErrors = tt.requests, tt.errors
			if tt.requests > 0 {
				e.Metrics.ErrorRatePct = float64(tt.errors) / float64(tt.requests) * 100
			}
			out := Evaluate(e)
			if out.Status != tt.status || out.Requests == nil || !out.Requests.Known || out.Requests.ServerErrors != tt.errors {
				t.Fatalf("%+v", out)
			}
			check := out.Checks[len(out.Checks)-1]
			if check.Reason != tt.reason || out.Requests.Policy.MinimumRequests != api.AppHealthMinRequests {
				t.Fatalf("%+v", check)
			}
		})
	}
}
