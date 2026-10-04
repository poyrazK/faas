// adr: 531
package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDeploymentSmokePeerRequiresScopedReadiness(t *testing.T) {
	for _, scenario := range []string{"disabled", "ready", "unready", "missing source", "missing configuration", "wrong owner", "read error"} {
		t.Run(scenario, func(t *testing.T) {
			target := Target{AppID: "app", DeploymentID: "candidate", InstanceID: "peer", NodeID: "node", WakeID: "wake", Port: 9090, AddedAt: time.Now()}
			reads := 0
			failure := errors.New("readiness unavailable")
			var placementDeadline time.Time
			backend := NewPGBackend(nil, nil, nil).WithDeploymentSmokeTargetLoader(func(ctx context.Context, app, deployment string) (Target, bool, error) {
				if app != target.AppID || deployment != target.DeploymentID {
					t.Fatalf("candidate read escaped identity: app=%s deployment=%s", app, deployment)
				}
				var bounded bool
				placementDeadline, bounded = ctx.Deadline()
				if !bounded || time.Until(placementDeadline) > api.TrafficPlacementReadTimeout {
					t.Error("candidate placement has no platform read bound")
				}
				return target, true, nil
			}).WithTargetReadinessLoader(func(ctx context.Context, targets []Target) (map[string]TargetReadinessSnapshot, error) {
				reads++
				deadline, bounded := ctx.Deadline()
				if !bounded || !deadline.Equal(placementDeadline) || len(targets) != 1 || !sameTargetPlacement(target, targets[0]) {
					t.Fatal("readiness did not share the placement deadline and exact identity")
				}
				if scenario == "read error" {
					return nil, failure
				}
				if scenario == "missing configuration" {
					return nil, nil
				}
				snapshot := TargetReadinessSnapshot{AppID: target.AppID, DeploymentID: target.DeploymentID, InstanceID: target.InstanceID, NodeID: target.NodeID, WakeID: target.WakeID,
					RequiredSources: []string{"primary", "sidecar:ingress"}, States: map[string]ReadinessState{"primary": {Ready: true, UpdatedAt: time.Now(), EventID: 1}, "sidecar:ingress": {Ready: true, UpdatedAt: time.Now(), EventID: 2}}}
				switch scenario {
				case "disabled":
					snapshot.RequiredSources, snapshot.States = nil, nil
				case "unready":
					snapshot.States["primary"] = ReadinessState{Ready: false, UpdatedAt: time.Now(), EventID: 3}
				case "missing source":
					delete(snapshot.States, "sidecar:ingress")
				case "wrong owner":
					snapshot.AppID = "other"
				}
				return map[string]TargetReadinessSnapshot{target.InstanceID: snapshot}, nil
			})
			resolved, found, err := backend.ResolveDeploymentSmokeTarget(t.Context(), target.AppID, target.DeploymentID)
			wantReady := scenario == "disabled" || scenario == "ready"
			if reads != 1 || found != wantReady || (wantReady && (err != nil || !resolved.hasReadinessConfiguration() || !resolved.routeReady())) || (!wantReady && err == nil) {
				t.Fatalf("peer readiness bypassed: scenario=%s reads=%d found=%t ready=%t err=%v", scenario, reads, found, resolved.routeReady(), err)
			}
			if scenario == "read error" && !errors.Is(err, failure) {
				t.Fatalf("read failure lost cause: %v", err)
			}
			if backend.CapacityCount(target.AppID) != 0 || backend.PickForDeployment(target.AppID, target.DeploymentID).OK {
				t.Fatal("private smoke candidate entered ordinary customer capacity")
			}
		})
	}
}

func TestDeploymentSmokePeerCanceledBeforeRead(t *testing.T) {
	reads := 0
	backend := NewPGBackend(nil, nil, nil).WithDeploymentSmokeTargetLoader(func(context.Context, string, string) (Target, bool, error) {
		reads++
		return Target{AppID: "app", DeploymentID: "candidate", InstanceID: "peer", NodeID: "node"}, true, nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	target, found, err := backend.ResolveDeploymentSmokeTarget(ctx, "app", "candidate")
	if reads != 0 || found || target.InstanceID != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled candidate read continued: reads=%d found=%t target=%+v err=%v", reads, found, target, err)
	}
}

func TestDeploymentSmokePeerRejectsInvalidPort(t *testing.T) {
	for _, port := range []int{-1, 65536} {
		backend := NewPGBackend(nil, nil, nil).WithDeploymentSmokeTargetLoader(func(context.Context, string, string) (Target, bool, error) {
			return Target{AppID: "app", DeploymentID: "candidate", InstanceID: "peer", NodeID: "node", Port: port}, true, nil
		})
		if target, found, err := backend.ResolveDeploymentSmokeTarget(t.Context(), "app", "candidate"); found || err == nil || target.InstanceID != "" {
			t.Fatalf("invalid candidate runtime port allowed: port=%d target=%+v found=%t err=%v", port, target, found, err)
		}
	}
}
