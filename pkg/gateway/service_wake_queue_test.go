// adr: 375
package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

type serviceWakeQueueFixture struct {
	handler *Handler
	backend *publicSnapshotBackend
	started chan string
	release chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

func newServiceWakeQueueFixture(t *testing.T) *serviceWakeQueueFixture {
	t.Helper()
	h, backend, _ := newTestHandler(t)
	f := &serviceWakeQueueFixture{handler: h, backend: &publicSnapshotBackend{fakeBackend: backend},
		started: make(chan string, 16), release: make(chan struct{})}
	h.backend = f.backend
	f.backend.onAdmit = func(ctx context.Context, deployment, _ string, _ int) error {
		f.calls.Add(1)
		f.started <- deployment
		select {
		case <-f.release:
		case <-ctx.Done():
			return ctx.Err()
		}
		backend.AddTarget(Target{InstanceID: deployment, DeploymentID: deployment, NodeID: "node"})
		return nil
	}
	t.Cleanup(f.unblock)
	return f
}

func (f *serviceWakeQueueFixture) unblock() { f.once.Do(func() { close(f.release) }) }

func (f *serviceWakeQueueFixture) wake(ctx context.Context, app App, deployment string) <-chan error {
	result := make(chan error, 1)
	go func() { result <- f.handler.EnsureServiceDeploymentCapacity(ctx, app, deployment) }()
	return result
}

func serviceWakeAwait(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !ready() {
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("wake state did not reach the expected boundary")
		}
	}
}

func serviceWakeResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatal("wake caller did not finish")
		return nil
	}
}

func TestServiceDeploymentWakeSharesBoundedAppQueue(t *testing.T) {
	for _, overflow := range []string{api.ConcurrencyOverflowQueue, api.ConcurrencyOverflowDrop} {
		t.Run(overflow, func(t *testing.T) {
			f := newServiceWakeQueueFixture(t)
			app := f.backend.app
			app.WakeMaxQueueDepth, app.ConcurrencyOverflow = 2, overflow
			first := f.wake(t.Context(), app, "selected")
			serviceWakeAwait(t, func() bool { return f.calls.Load() == 1 })
			var second <-chan error
			if overflow == api.ConcurrencyOverflowQueue {
				second = f.wake(t.Context(), app, "selected")
				// Also observe a bypassed Admit so the baseline failure is
				// reported after every blocked fixture caller is released.
				serviceWakeAwait(t, func() bool { return f.handler.gate.InflightWaiters(app.ID) == 2 || f.calls.Load() == 2 })
			}
			rejected := f.wake(t.Context(), app, "selected")
			var refusal error
			var prompt bool
			select {
			case refusal = <-rejected:
				prompt = true
			case <-time.After(100 * time.Millisecond):
			}
			f.unblock()
			if !prompt {
				refusal = serviceWakeResult(t, rejected)
			}
			if err := serviceWakeResult(t, first); err != nil {
				t.Fatal(err)
			}
			if second != nil {
				if err := serviceWakeResult(t, second); err != nil {
					t.Fatal(err)
				}
			}
			if !prompt || !errors.Is(refusal, ErrQueueFull) || f.calls.Load() != 1 {
				t.Fatalf("managed wake bypassed app queue: prompt=%v err=%v admissions=%d", prompt, refusal, f.calls.Load())
			}
			if (overflow == api.ConcurrencyOverflowDrop) != isWakeConcurrencyDrop(refusal) {
				t.Fatalf("wake refusal lost the configured overflow mode: %v", refusal)
			}
			if got := f.handler.gate.InflightWaiters(app.ID); got != 0 || f.handler.gate.WakeInProgress(app.ID) {
				t.Fatalf("completed wake retained waiters: %d", got)
			}
		})
	}
}

func TestServiceDeploymentWakeExpiredCallerCannotStartAdmission(t *testing.T) {
	f := newServiceWakeQueueFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := f.handler.EnsureServiceDeploymentCapacity(ctx, f.backend.app, "selected"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expired caller: %v", err)
	}
	if f.calls.Load() != 0 || f.handler.gate.WakeInProgress(f.backend.app.ID) {
		t.Fatal("expired caller started shared wake work")
	}
}

func TestServiceDeploymentWakeCancellationReleasesWaiterAndKeepsSharedLeader(t *testing.T) {
	f := newServiceWakeQueueFixture(t)
	app := f.backend.app
	app.WakeMaxQueueDepth = 2
	ctx, cancel := context.WithCancel(t.Context())
	first := f.wake(ctx, app, "selected")
	serviceWakeAwait(t, func() bool { return f.calls.Load() == 1 })
	second := f.wake(t.Context(), app, "selected")
	serviceWakeAwait(t, func() bool { return f.handler.gate.InflightWaiters(app.ID) == 2 })
	cancel()
	if err := serviceWakeResult(t, first); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter: %v", err)
	}
	if f.handler.gate.InflightWaiters(app.ID) != 1 || !f.handler.gate.WakeInProgress(app.ID) {
		t.Fatal("cancellation removed another caller's wake")
	}
	third := f.wake(t.Context(), app, "selected")
	serviceWakeAwait(t, func() bool { return f.handler.gate.InflightWaiters(app.ID) == 2 })
	f.unblock()
	for _, result := range []<-chan error{second, third} {
		if err := serviceWakeResult(t, result); err != nil {
			t.Fatal(err)
		}
	}
	if f.calls.Load() != 1 || f.handler.gate.InflightWaiters(app.ID) != 0 {
		t.Fatal("replacement waiter duplicated admission or retained a queue slot")
	}
}

func TestServiceDeploymentWakeDeadlineDoesNotDuplicateOrphanedLeader(t *testing.T) {
	for _, bound := range []string{"caller", "app"} {
		t.Run(bound, func(t *testing.T) {
			f := newServiceWakeQueueFixture(t)
			app := f.backend.app
			ctx := t.Context()
			if bound == "caller" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 30*time.Millisecond)
				defer cancel()
			} else {
				app.MaxQueueWaitMS = 30
			}
			first := f.wake(ctx, app, "selected")
			serviceWakeAwait(t, func() bool { return f.calls.Load() == 1 })
			err := serviceWakeResult(t, first)
			if (bound == "caller" && !errors.Is(err, context.DeadlineExceeded)) || (bound == "app" && !errors.Is(err, ErrWakeQueueWaitTimeout)) {
				t.Fatalf("wrong expiry: %v", err)
			}
			if f.handler.gate.InflightWaiters(app.ID) != 0 || !f.handler.gate.WakeInProgress(app.ID) {
				t.Fatal("expired caller retained a waiter or abandoned the bounded leader")
			}
			app.MaxQueueWaitMS = 0
			second := f.wake(t.Context(), app, "selected")
			serviceWakeAwait(t, func() bool { return f.handler.gate.InflightWaiters(app.ID) == 1 })
			f.unblock()
			if err := serviceWakeResult(t, second); err != nil || f.calls.Load() != 1 {
				t.Fatalf("new caller did not reuse shared admission: %v/%d", err, f.calls.Load())
			}
		})
	}
}

func TestServiceDeploymentWakeUsesGatewayAdmissionQueue(t *testing.T) {
	f := newServiceWakeQueueFixture(t)
	h := f.handler
	h.admissionQueue = newWakeAdmissionQueue(1, 1, nil)
	holderStarted, holderRelease := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(holderRelease) }) }
	t.Cleanup(unblock)
	holderResult := make(chan error, 1)
	go func() {
		_, _, err := h.admissionQueue.Do(t.Context(), "holder", "pro", WakeAdmissionPolicyForPlan(api.PlanPro), func(ctx context.Context) error {
			close(holderStarted)
			select {
			case <-holderRelease:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		holderResult <- err
	}()
	<-holderStarted
	app := f.backend.app
	first := f.wake(t.Context(), app, "selected")
	serviceWakeAwait(t, func() bool {
		h.admissionQueue.mu.Lock()
		defer h.admissionQueue.mu.Unlock()
		return h.admissionQueue.waiting.Len() == 1
	})
	other := app
	other.ID = "other-app"
	if err := serviceWakeResult(t, f.wake(t.Context(), other, "other-deployment")); !errors.Is(err, ErrWakeAdmissionQueueFull) {
		t.Fatalf("managed wake bypassed full gateway queue: %v", err)
	}
	if f.calls.Load() != 0 {
		t.Fatal("queued managed wake reached scheduler before a slot was available")
	}
	unblock()
	if err := serviceWakeResult(t, holderResult); err != nil {
		t.Fatal(err)
	}
	serviceWakeAwait(t, func() bool { return f.calls.Load() == 1 })
	f.unblock()
	if err := serviceWakeResult(t, first); err != nil {
		t.Fatal(err)
	}
	h.admissionQueue.mu.Lock()
	defer h.admissionQueue.mu.Unlock()
	if h.admissionQueue.running != 0 || h.admissionQueue.waiting.Len() != 0 {
		t.Fatal("managed wake retained gateway admission capacity")
	}
}

func TestServiceDeploymentWakeSharesPublicCohortGeneration(t *testing.T) {
	for _, same := range []bool{true, false} {
		name := "same-cohort"
		if !same {
			name = "different-cohort"
		}
		t.Run(name, func(t *testing.T) {
			f := newServiceWakeQueueFixture(t)
			app := f.backend.app
			public := make(chan error, 1)
			go func() {
				_, _, _, err := f.handler.wakeDeployment(t.Context(), app, "public", app.Scope, sched.TriggerGateway, 5)
				public <- err
			}()
			serviceWakeAwait(t, func() bool { return f.calls.Load() == 1 })
			selected := "public"
			if !same {
				selected = "managed"
			}
			managed := f.wake(t.Context(), app, selected)
			serviceWakeAwait(t, func() bool { return f.handler.gate.InflightWaiters(app.ID) == 2 })
			if f.calls.Load() != 1 {
				t.Fatal("managed cohort did not share the app generation")
			}
			f.unblock()
			for _, result := range []<-chan error{public, managed} {
				if err := serviceWakeResult(t, result); err != nil {
					t.Fatal(err)
				}
			}
			want := int32(1)
			if !same {
				want = 2
			}
			if f.calls.Load() != want || !f.backend.PickForDeployment(app.ID, selected).OK {
				t.Fatalf("cohort wake changed: calls=%d selected=%s", f.calls.Load(), selected)
			}
		})
	}
}

func TestServiceDeploymentWakeUsesAppCeilingAndAdmittedRollout(t *testing.T) {
	for _, mode := range []string{"single", "cold-sibling", "warm-sibling", "release", "override", "foreign-policy", "changed-selection"} {
		t.Run(mode, func(t *testing.T) {
			f := newServiceWakeQueueFixture(t)
			app := f.backend.app
			app.MaxConcurrency = 1
			routing := &ServiceRoutingSnapshot{SelectedDeploymentID: "selected", Weights: []DeploymentWeightsRow{{ID: "selected", TrafficPercent: 100}}}
			snapshot := ServicePolicySnapshot{Target: ServiceTarget{AppID: app.ID}, Routing: routing}
			want := 1
			switch mode {
			case "cold-sibling", "warm-sibling":
				routing.Weights = []DeploymentWeightsRow{{ID: "selected", TrafficPercent: 50}, {ID: "sibling", TrafficPercent: 50}}
				if mode == "warm-sibling" {
					f.backend.AddTarget(Target{InstanceID: "sibling", DeploymentID: "sibling", NodeID: "node"})
					want += api.RolloutConcurrencyGrant
				}
			case "release":
				routing.ReleaseDeploymentID = "selected"
				want += api.RolloutConcurrencyGrant
			case "override":
				routing.OverrideChecked, routing.OverrideAllowed, routing.OverrideDeploymentID = true, true, "selected"
				want += api.RolloutConcurrencyGrant
			case "foreign-policy":
				snapshot.Target.AppID, routing.ReleaseDeploymentID = "foreign", "selected"
			case "changed-selection":
				routing.SelectedDeploymentID, routing.ReleaseDeploymentID = "other", "selected"
			}
			f.backend.onAdmit = func(_ context.Context, deployment, scope string, maximum int) error {
				if maximum != want || deployment != "selected" || scope != app.Scope {
					t.Errorf("wrong managed admission: maximum=%d deployment=%s scope=%s", maximum, deployment, scope)
				}
				return nil
			}
			ctx := context.WithValue(t.Context(), pinnedServicePolicyKey{}, pinnedServicePolicy{snapshot: snapshot})
			if err := f.handler.EnsureServiceDeploymentCapacity(ctx, app, "selected"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestServiceProxyWakeAdmissionRefusalsPreserveRetryContract(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"app-full", &WakeQueueFullError{Depth: 2, Limit: 2, RetryAfter: 1500 * time.Millisecond}, http.StatusServiceUnavailable, api.CodeCapacity},
		{"drop", &WakeConcurrencyDropError{RetryAfter: 1500 * time.Millisecond}, http.StatusTooManyRequests, api.CodeConcurrencyThrottled},
		{"app-timeout", &WakeQueueWaitTimeoutError{RetryAfter: 1500 * time.Millisecond}, http.StatusServiceUnavailable, api.CodeCapacity},
		{"gateway-full", &WakeAdmissionQueueFullError{Depth: 2, Limit: 2, RetryAfter: 1500 * time.Millisecond}, http.StatusServiceUnavailable, api.CodeCapacity},
		{"gateway-timeout", &WakeAdmissionQueueWaitTimeoutError{RetryAfter: 1500 * time.Millisecond}, http.StatusServiceUnavailable, api.CodeCapacity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proxy := newMeteredProxy(t, NewMetrics(), staticProvider{}, func(context.Context, string) error { return tc.err }, nil)
			response := meteredGET(t, proxy)
			if response.Code != tc.status || response.Header().Get("Retry-After") != "2" || !containsProblemCode(response, tc.code) {
				t.Fatalf("wake refusal lost contract: %d %v %s", response.Code, response.Header(), response.Body)
			}
			if callCount(t, proxy.metrics, ServiceCallWakeQueueFull) != 1 {
				t.Fatal("queue refusal was reported as a wake failure")
			}
		})
	}
}

func containsProblemCode(response *httptest.ResponseRecorder, code string) bool {
	return strings.Contains(response.Body.String(), `"code":"`+code+`"`)
}
