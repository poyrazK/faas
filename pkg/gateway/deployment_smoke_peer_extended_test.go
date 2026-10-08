// adr: 570
package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
)

func TestDeploymentSmokePeerNotificationDuringReadRefuses(t *testing.T) {
	target := Target{AppID: "app", DeploymentID: "candidate", InstanceID: "peer", NodeID: "node", WakeID: "wake", Port: 9090}
	backend := NewPGBackend(nil, nil, nil).WithDeploymentSmokeTargetLoader(func(context.Context, string, string) (Target, bool, error) {
		return target, true, nil
	})
	at := time.Now()
	backend.WithTargetReadinessLoader(func(context.Context, []Target) (map[string]TargetReadinessSnapshot, error) {
		backend.SetInstanceReadinessForTarget(target.AppID, target.InstanceID, target.WakeID, target.NodeID, "primary", "unready", at.Add(time.Second), 2)
		return map[string]TargetReadinessSnapshot{target.InstanceID: {AppID: target.AppID, InstanceID: target.InstanceID, DeploymentID: target.DeploymentID, NodeID: target.NodeID, WakeID: target.WakeID,
			RequiredSources: []string{"primary"}, States: map[string]ReadinessState{"primary": {Ready: true, UpdatedAt: at, EventID: 1}}}}, nil
	})
	if _, found, err := backend.ResolveDeploymentSmokeTarget(t.Context(), target.AppID, target.DeploymentID); found || !errors.Is(err, ErrTargetReadinessUnavailable) {
		t.Fatalf("older read masked current withdrawal: found=%t err=%v", found, err)
	}
	if backend.CapacityCount(target.AppID) != 0 {
		t.Fatal("private candidate became customer capacity")
	}
}

func TestDeploymentSmokePeerCanceledDuringRead(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	readinessReads := 0
	backend := NewPGBackend(nil, nil, nil).WithDeploymentSmokeTargetLoader(func(context.Context, string, string) (Target, bool, error) {
		cancel()
		return Target{AppID: "app", DeploymentID: "candidate", InstanceID: "peer", NodeID: "node"}, true, nil
	}).WithTargetReadinessLoader(func(context.Context, []Target) (map[string]TargetReadinessSnapshot, error) {
		readinessReads++
		return nil, nil
	})
	if _, found, err := backend.ResolveDeploymentSmokeTarget(ctx, "app", "candidate"); found || !errors.Is(err, context.Canceled) || readinessReads != 0 {
		t.Fatalf("canceled placement proceeded to readiness: found=%t reads=%d err=%v", found, readinessReads, err)
	}
}

type smokePeerHandlerBackend struct {
	*fakeBackend
	peer *PGBackend
}

func (b *smokePeerHandlerBackend) ValidateDeploymentSmoke(app, deployment, token string) bool {
	return b.peer.ValidateDeploymentSmoke(app, deployment, token)
}

func (b *smokePeerHandlerBackend) ResolveDeploymentSmokeTarget(ctx context.Context, app, deployment string) (Target, bool, error) {
	return b.peer.ResolveDeploymentSmokeTarget(ctx, app, deployment)
}

func TestDeploymentSmokePeerHandlerRefusesBeforeForwardingOrWake(t *testing.T) {
	for _, scenario := range []string{"ready", "unready", "missing configuration", "read error"} {
		t.Run(scenario, func(t *testing.T) {
			fake := &fakeBackend{app: App{ID: "app", AccountID: "account", Plan: api.PlanFree, MaxConcurrency: 1, HealthPath: "/healthz"}, host: "demo.apps.dom"}
			fake.AddTarget(Target{AppID: "app", DeploymentID: "stable", InstanceID: "stable-instance", NodeID: "stable-node"})
			target := Target{AppID: "app", DeploymentID: "candidate", InstanceID: "peer", NodeID: "node", WakeID: "wake", Port: 9090, AddedAt: time.Now()}
			peer := NewPGBackend(nil, nil, nil).WithDeploymentSmokeTargetLoader(func(context.Context, string, string) (Target, bool, error) {
				return target, true, nil
			}).WithTargetReadinessLoader(func(context.Context, []Target) (map[string]TargetReadinessSnapshot, error) {
				if scenario == "read error" {
					return nil, errors.New("readiness store unavailable")
				}
				if scenario == "missing configuration" {
					return nil, nil
				}
				return map[string]TargetReadinessSnapshot{target.InstanceID: {AppID: target.AppID, InstanceID: target.InstanceID, DeploymentID: target.DeploymentID, NodeID: target.NodeID, WakeID: target.WakeID,
					RequiredSources: []string{"primary"}, States: map[string]ReadinessState{"primary": {Ready: scenario == "ready", UpdatedAt: time.Now(), EventID: 1}}}}, nil
			})
			peer.AuthorizeDeploymentSmoke(target.AppID, target.DeploymentID, "challenge", time.Now().Add(time.Minute))
			backend := &smokePeerHandlerBackend{fakeBackend: fake, peer: peer}
			forwards := 0
			handler := NewHandlerWith(backend, NewMetrics(), nil).WithForwarding(func(selected Target) http.Handler {
				forwards++
				if selected.InstanceID != target.InstanceID || selected.DeploymentID != target.DeploymentID || selected.Port != target.Port {
					t.Fatalf("smoke fell back to sibling: target=%+v", selected)
				}
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
			})
			request := httptest.NewRequest(http.MethodGet, "http://demo.apps.dom/healthz", nil)
			request.Header.Set(apihostingreceipt.PlatformSmokeHeader, "1")
			request.Header.Set(apihostingreceipt.PlatformSmokeDeploymentHeader, target.DeploymentID)
			request.Header.Set(apihostingreceipt.PlatformSmokeTokenHeader, "challenge")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			wantStatus, wantForwards := http.StatusServiceUnavailable, 0
			if scenario == "ready" {
				wantStatus, wantForwards = http.StatusNoContent, 1
			}
			if response.Code != wantStatus || forwards != wantForwards || *fake.Admits() != 0 || peer.CapacityCount(target.AppID) != 0 {
				t.Fatalf("candidate refusal or private capacity changed: status=%d forwards=%d admits=%d publicCapacity=%d", response.Code, forwards, *fake.Admits(), peer.CapacityCount(target.AppID))
			}
		})
	}
}
