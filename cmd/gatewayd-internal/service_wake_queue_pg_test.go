// adr: 570
package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type queuedServiceScheduler struct {
	gateway.NoopScheduler
	release chan struct{}
	once    sync.Once
	calls   atomic.Int32
	app     string
}

func (s *queuedServiceScheduler) unblock() { s.once.Do(func() { close(s.release) }) }

func (s *queuedServiceScheduler) AdmitInstance(ctx context.Context, app, deployment, scope, trigger string) (string, string, string, string, int32, bool, int, error) {
	s.calls.Add(1)
	if app != s.app || deployment == "" || scope != "production" || trigger != sched.TriggerServiceMesh {
		return "", "", "", "", 0, false, 0, fmt.Errorf("unexpected queued service identity: %s/%s/%s/%s", app, deployment, scope, trigger)
	}
	select {
	case <-s.release:
	case <-ctx.Done():
		return "", "", "", "", 0, false, 0, ctx.Err()
	}
	return "instance-" + deployment, "node", deployment, "wake-" + deployment, 0, false, 8080, nil
}

// The production policy reader, app/deployment projection, Handler wake gate,
// PGBackend publication and ServiceProxy run together. Scheduler lifecycle and
// the final forwarder are explicit fixtures; this is not native VM acceptance.
func TestServiceDeploymentWakePostgresPinsCohortAndBoundsFanIn(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(t.Context(), "queued-service@test.local", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	createApp := func(slug string) state.App {
		t.Helper()
		app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: slug, Type: state.AppTypeApp, RAMMB: 128})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	caller, target := createApp("queued-client"), createApp("queued-target")
	policy := state.ScalingPolicy{WakeMaxQueueDepth: 2}
	maximum := 1
	updated, err := store.UpdateApp(t.Context(), target.ID, state.UpdateAppParams{MaxConcurrency: &maximum, ScalingPolicy: &policy, SetScalingPolicy: true})
	if err != nil {
		t.Fatal(err)
	}
	if updated.MaxConcurrency != 1 || updated.ScalingPolicy == nil || updated.ScalingPolicy.WakeMaxQueueDepth != 2 {
		t.Fatalf("stored queue fixture differs from requested settings: %+v", updated)
	}
	createDeployment := func(digest string) state.Deployment {
		t.Helper()
		deployment, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: target.ID, Scope: "production", Kind: state.DeploymentKindImage,
			ImageDigest: digest, Status: state.DeployPending, TrafficPercent: 100})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(t.Context(), deployment.ID); err != nil {
			t.Fatal(err)
		}
		return deployment
	}
	old := createDeployment("sha256:queued-old")
	scheduler := &queuedServiceScheduler{app: target.ID, release: make(chan struct{})}
	t.Cleanup(scheduler.unblock)
	backend := gateway.NewPGBackend(pgRouter{store: store}, scheduler, discardLogger()).WithStore(weightsStoreAdapter{store: store})
	metrics := gateway.NewMetrics()
	handler := gateway.NewHandlerWith(backend, metrics, discardLogger())
	handler.SetWakeGateHook()
	var forwards atomic.Int32
	proxy := gateway.NewServiceProxy(gateway.ServiceProxyConfig{Provider: backend, Policy: newServicePolicyPinner(store), Metrics: metrics,
		Wake:           newServiceProxyWaker(store, handler.EnsureServiceCapacity),
		WakeDeployment: newServiceProxyDeploymentWaker(store, handler.EnsureServiceDeploymentCapacity),
		Forward: func(target gateway.Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				forwards.Add(1)
				w.Header().Set("Observed-Deployment", target.DeploymentID)
				w.Header().Set("Observed-Policy", gateway.TrafficPolicyRevision(r.Context()))
				w.WriteHeader(http.StatusNoContent)
			})
		},
	})
	request := func(ctx context.Context) <-chan *httptest.ResponseRecorder {
		result := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			r := httptest.NewRequestWithContext(ctx, http.MethodGet, "http://gateway/v1/internal/services/"+target.Slug+"/work", nil)
			r.Header.Set(gateway.ServiceProxyCallerAppHeader, caller.ID)
			w := httptest.NewRecorder()
			proxy.ServeHTTP(w, r)
			result <- w
		}()
		return result
	}
	await := func(ready func() bool) {
		t.Helper()
		deadline := time.NewTimer(2 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for !ready() {
			select {
			case <-tick.C:
			case <-deadline.C:
				t.Fatal("queued service did not reach the expected boundary")
			}
		}
	}
	depth := func() int {
		families, err := metrics.Registry().Gather()
		if err != nil {
			t.Fatal(err)
		}
		for _, family := range families {
			if family.GetName() != "gateway_wake_queue_depth" {
				continue
			}
			for _, metric := range family.Metric {
				for _, label := range metric.Label {
					if label.GetName() == "app" && label.GetValue() == target.ID {
						return int(metric.GetGauge().GetValue())
					}
				}
			}
		}
		return 0
	}
	response := func(result <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
		t.Helper()
		select {
		case w := <-result:
			return w
		case <-time.After(2 * time.Second):
			t.Fatal("queued service did not return")
			return nil
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first := request(ctx)
	await(func() bool { return scheduler.calls.Load() == 1 && depth() == 1 })
	second := request(t.Context())
	await(func() bool { return depth() == 2 })
	full := response(request(t.Context()))
	if full.Code != http.StatusServiceUnavailable || full.Header().Get("Retry-After") == "" || !strings.Contains(full.Body.String(), `"code":"`+api.CodeCapacity+`"`) {
		t.Fatalf("stored wake depth did not refuse excess call: %d %s", full.Code, full.Body)
	}
	cancel()
	_ = response(first)
	await(func() bool { return depth() == 1 })
	fourth := request(t.Context())
	await(func() bool { return depth() == 2 })
	if scheduler.calls.Load() != 1 || forwards.Load() != 0 || pool.Stat().AcquiredConns() != 0 {
		t.Fatalf("queued call duplicated admission, forwarded early or retained transaction: admissions=%d forwards=%d acquired=%d", scheduler.calls.Load(), forwards.Load(), pool.Stat().AcquiredConns())
	}
	next := createDeployment("sha256:queued-new")
	scheduler.unblock()
	var admittedRevision string
	for _, result := range []<-chan *httptest.ResponseRecorder{second, fourth} {
		w := response(result)
		if admittedRevision == "" {
			admittedRevision = w.Header().Get("Observed-Policy")
		}
		if w.Code != http.StatusNoContent || w.Header().Get("Observed-Deployment") != old.ID || w.Header().Get("Observed-Policy") != admittedRevision || !strings.HasPrefix(admittedRevision, "traffic-v1:") {
			t.Fatalf("queued call changed its admitted cohort: %d %v %s", w.Code, w.Header(), w.Body)
		}
	}
	if scheduler.calls.Load() != 1 || forwards.Load() != 2 || backend.CapacityCount(target.ID) != 1 {
		t.Fatalf("coalesced wake: admissions=%d forwards=%d capacity=%d", scheduler.calls.Load(), forwards.Load(), backend.CapacityCount(target.ID))
	}
	blocked := response(request(t.Context()))
	if blocked.Code != http.StatusTooManyRequests || !strings.Contains(blocked.Body.String(), `"code":"`+api.CodeAppConcurReached+`"`) || !strings.Contains(blocked.Body.String(), `"observed":1`) || scheduler.calls.Load() != 1 || forwards.Load() != 2 {
		t.Fatalf("fresh cold cohort bypassed app ceiling or used retired sibling: %d %s admissions=%d forwards=%d", blocked.Code, blocked.Body, scheduler.calls.Load(), forwards.Load())
	}
	backend.EvictInstance(target.ID, "instance-"+old.ID)
	w := response(request(t.Context()))
	if w.Code != http.StatusNoContent || w.Header().Get("Observed-Deployment") != next.ID || w.Header().Get("Observed-Policy") == admittedRevision || scheduler.calls.Load() != 2 || forwards.Load() != 3 {
		t.Fatalf("fresh service did not use new cohort: %d %v %s admissions=%d forwards=%d", w.Code, w.Header(), w.Body, scheduler.calls.Load(), forwards.Load())
	}
	await(func() bool { return depth() == 0 })
	if pool.Stat().AcquiredConns() != 0 {
		t.Fatal("completed service retained a transaction")
	}
}
