// adr: 20261105115100
package ingressroute

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type readinessFixture struct {
	rows fixedDeploymentSource
	read func(context.Context, []state.ReadinessTarget) (map[string]map[string]state.InstanceReadiness, error)
}

func (f readinessFixture) LiveDeployments(ctx context.Context, app string) ([]state.Deployment, error) {
	return f.rows.LiveDeployments(ctx, app)
}
func (f readinessFixture) LatestInstanceReadinessForTargets(ctx context.Context, targets []state.ReadinessTarget) (map[string]map[string]state.InstanceReadiness, error) {
	return f.read(ctx, targets)
}

func TestRawReadinessRequiredSources(t *testing.T) {
	for _, tc := range []struct {
		name, primary, sidecars string
		observations            []string
		want                    bool
	}{
		{"disabled", `{}`, `[]`, nil, true},
		{"primary missing", `{"path":"/readyz"}`, `[]`, nil, false},
		{"primary ready", `{"path":"/readyz"}`, `[]`, []string{"primary_app"}, true},
		{"grpc missing", `{"grpc":{"port":9000}}`, `[]`, nil, false},
		{"ingress missing", `{}`, `[{"name":"proxy","type":"sidecar","primary_ingress":true,"readiness_probe":{"path":"/ready"}}]`, nil, false},
		{"ingress ready", `{}`, `[{"name":"proxy","type":"sidecar","primary_ingress":true,"readiness_probe":{"path":"/ready"}}]`, []string{"sidecar:proxy"}, true},
		{"both required", `{"path":"/readyz"}`, `[{"name":"proxy","type":"sidecar","primary_ingress":true,"readiness_probe":{"path":"/ready"}}]`, []string{"primary_app"}, false},
		{"both ready", `{"path":"/readyz"}`, `[{"name":"proxy","type":"sidecar","primary_ingress":true,"readiness_probe":{"path":"/ready"}}]`, []string{"primary_app", "sidecar:proxy"}, true},
		{"auxiliary ignored", `{}`, `[{"name":"metrics","type":"sidecar","readiness_probe":{"path":"/ready"}},{"name":"setup","type":"init","primary_ingress":true,"readiness_probe":{"path":"/ready"}}]`, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := state.Deployment{ID: "dep", AppID: "app", Status: state.DeployLive, TrafficPercent: 100, OverrideReadinessProbe: json.RawMessage(tc.primary), Sidecars: json.RawMessage(tc.sidecars)}
			target := state.Instance{ID: "instance", AppID: "app", DeploymentID: "dep", WakeID: "wake", NodeID: "node"}
			source := readinessFixture{rows: fixedDeploymentSource{row}, read: func(_ context.Context, targets []state.ReadinessTarget) (map[string]map[string]state.InstanceReadiness, error) {
				if len(targets) != 1 || targets[0] != (state.ReadinessTarget{AppID: "app", InstanceID: "instance", WakeID: "wake", NodeID: "node"}) {
					t.Fatalf("wrong identities: %+v", targets)
				}
				states := map[string]state.InstanceReadiness{}
				for _, name := range tc.observations {
					states[name] = state.InstanceReadiness{Ready: true, At: time.Now()}
				}
				return map[string]map[string]state.InstanceReadiness{"instance": states}, nil
			}}
			ready, err := ReadyInstances(t.Context(), source, "app", "dep", []state.Instance{target})
			if err != nil || (len(ready) == 1) != tc.want {
				t.Fatalf("ready=%+v error=%v want=%v", ready, err, tc.want)
			}
		})
	}
}

func TestRawReadinessFailures(t *testing.T) {
	failure := errors.New("read failed")
	for _, mode := range []string{"missing deployment", "staging deployment", "bad primary", "bad sidecars", "reader error", "deadline", "zero timestamp", "wrong target"} {
		t.Run(mode, func(t *testing.T) {
			row := state.Deployment{ID: "dep", AppID: "app", Status: state.DeployLive, TrafficPercent: 100, OverrideReadinessProbe: json.RawMessage(`{"path":"/readyz"}`)}
			target := state.Instance{ID: "instance", AppID: "app", DeploymentID: "dep", WakeID: "wake", NodeID: "node"}
			switch mode {
			case "missing deployment":
				row.ID = "other"
			case "staging deployment":
				row.Scope = "staging"
			case "bad primary":
				row.OverrideReadinessProbe = json.RawMessage(`{`)
			case "bad sidecars":
				row.Sidecars = json.RawMessage(`{`)
			case "wrong target":
				target.AppID = "other"
			}
			source := readinessFixture{rows: fixedDeploymentSource{row}, read: func(ctx context.Context, _ []state.ReadinessTarget) (map[string]map[string]state.InstanceReadiness, error) {
				if mode == "reader error" {
					return nil, failure
				}
				if mode == "deadline" {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				return map[string]map[string]state.InstanceReadiness{"instance": {"primary_app": {Ready: true}}}, nil
			}}
			ctx := t.Context()
			if mode == "deadline" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 10*time.Millisecond)
				defer cancel()
			}
			ready, err := ReadyInstances(ctx, source, "app", "dep", []state.Instance{target})
			if len(ready) != 0 || (err == nil && mode != "zero timestamp") {
				t.Fatalf("ready=%+v error=%v", ready, err)
			}
			if mode == "reader error" && !errors.Is(err, failure) {
				t.Fatalf("lost read error: %v", err)
			}
			if mode == "deadline" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("lost timeout: %v", err)
			}
		})
	}
	rows := fixedDeploymentSource{{ID: "dep", AppID: "app", Status: state.DeployLive, TrafficPercent: 100, OverrideReadinessProbe: json.RawMessage(`{"path":"/readyz"}`)}}
	if _, err := ReadyInstances(t.Context(), rows, "app", "dep", nil); err == nil {
		t.Fatal("configured readiness without a reader was admitted")
	}
}
