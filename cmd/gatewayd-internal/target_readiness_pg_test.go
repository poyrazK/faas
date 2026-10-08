// adr: 570
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestTargetAdmissionRequiresStoredReadinessBeforeFirstNotification(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(t.Context(), "target-readiness@test.local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	create := func(slug string) state.App {
		t.Helper()
		app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: slug, Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	caller, target := create("target-readiness-caller"), create("target-readiness-service")
	deployment, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: target.ID, Scope: "production", Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:readiness", Status: state.DeployPending, TrafficPercent: 100,
		OverrideReadinessProbe: []byte(`{"path":"/readyz"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(t.Context(), deployment.ID); err != nil {
		t.Fatal(err)
	}
	scheduler := &queuedServiceScheduler{app: target.ID, release: make(chan struct{})}
	scheduler.unblock()
	backend := gateway.NewPGBackend(pgRouter{store: store}, scheduler, discardLogger()).WithStore(weightsStoreAdapter{store: store}).
		WithTargetReadinessLoader(newTargetReadinessLoader(store))
	handler := gateway.NewHandlerWith(backend, gateway.NewMetrics(), discardLogger())
	var forwarded atomic.Int32
	proxy := gateway.NewServiceProxy(gateway.ServiceProxyConfig{Provider: backend, Policy: newServicePolicyPinner(store),
		WakeDeployment: newServiceProxyDeploymentWaker(store, handler.EnsureServiceDeploymentCapacity),
		Forward: func(gateway.Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				forwarded.Add(1)
				w.WriteHeader(http.StatusNoContent)
			})
		},
	})
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://gateway/v1/internal/services/"+target.Slug+"/work", nil)
		r.Header.Set(gateway.ServiceProxyCallerAppHeader, caller.ID)
		w := httptest.NewRecorder()
		proxy.ServeHTTP(w, r)
		return w
	}
	w := request()
	if w.Code != http.StatusServiceUnavailable || forwarded.Load() != 0 || backend.HealthyCount(target.ID) != 0 || backend.CapacityCount(target.ID) != 1 || scheduler.calls.Load() != 1 {
		t.Fatalf("admission bypassed stored readiness before notifications: status=%d forwards=%d healthy=%d capacity=%d admissions=%d", w.Code, forwarded.Load(), backend.HealthyCount(target.ID), backend.CapacityCount(target.ID), scheduler.calls.Load())
	}
	w = request()
	if w.Code != http.StatusTooManyRequests || scheduler.calls.Load() != 1 || forwarded.Load() != 0 {
		t.Fatalf("unknown readiness re-admitted resident capacity: status=%d admits=%d forwards=%d", w.Code, scheduler.calls.Load(), forwarded.Load())
	}
	instance := "instance-" + deployment.ID
	publish := func(status string, at time.Time) {
		t.Helper()
		body, err := json.Marshal(map[string]string{"app_id": target.ID, "instance_id": instance, "wake_id": "wake-" + deployment.ID, "node_id": "node", "status": status})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AppendEventAt(t.Context(), "vmmd", "wake.app_readiness", &instance, body, at); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Now().UTC().Add(-time.Minute)
	for _, transition := range []struct {
		status string
		at     time.Time
		want   int
	}{
		{status: "ready", at: at, want: http.StatusNoContent},
		{status: "unready", at: at.Add(time.Second), want: http.StatusTooManyRequests},
		{status: "ready", at: at, want: http.StatusTooManyRequests},
		{status: "ready", at: at.Add(2 * time.Second), want: http.StatusNoContent},
	} {
		publish(transition.status, transition.at)
		if err := backend.ReconcileTargetReadiness(t.Context()); err != nil {
			t.Fatal(err)
		}
		w := request()
		if w.Code != transition.want || scheduler.calls.Load() != 1 || backend.CapacityCount(target.ID) != 1 {
			t.Fatalf("durable readiness transition=%+v status=%d admits=%d capacity=%d", transition, w.Code, scheduler.calls.Load(), backend.CapacityCount(target.ID))
		}
	}
	if forwarded.Load() != 2 {
		t.Fatalf("withdrawn or stale-ready event forwarded: count=%d", forwarded.Load())
	}
	t.Log("actual deployment metadata and source event SQL prevent readiness bypass, retain resident capacity and recover without any LISTEN subscriber; scheduler and final forward are fixtures")
}
