package gateway

// spec: §12

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPGBackendEnsureWarmRecordsSchedulerAndPublicationPhases(t *testing.T) {
	backend := NewPGBackend(nil, NewFakeScheduler("node-1").WithInstanceID("instance-1"), nil)
	trace := newWakePhaseTrace(time.Now())
	ctx := withWakePhaseTrace(context.Background(), trace)
	if _, _, atCapacity, err := backend.EnsureWarm(ctx, "app-1", "", "gateway"); err != nil || atCapacity {
		t.Fatalf("EnsureWarm: atCapacity=%v err=%v", atCapacity, err)
	}
	trace.markProxyStarted(time.Now())

	trace.mu.Lock()
	if trace.admissionStarted.IsZero() || trace.schedulerComplete.IsZero() || trace.targetPublished.IsZero() || trace.proxyStarted.IsZero() {
		trace.mu.Unlock()
		t.Fatalf("incomplete phase trace: %#v", trace)
	}
	if trace.schedulerComplete.Before(trace.admissionStarted) || trace.targetPublished.Before(trace.schedulerComplete) || trace.proxyStarted.Before(trace.targetPublished) {
		trace.mu.Unlock()
		t.Fatalf("phase timestamps out of order: %#v", trace)
	}
	trace.mu.Unlock()

	metrics := NewMetrics()
	trace.observe(metrics, time.Now())
	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	for _, phase := range []string{"pre_admission", "scheduler_wake", "target_publication", "post_publication", "internal_proxy"} {
		want := fmt.Sprintf(`gateway_wake_phase_duration_seconds_count{phase=%q} 1`, phase)
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("missing phase observation %q", want)
		}
	}
}

func TestWakePhaseTraceGatewayPhasesAreCorrelatedAtFirstByte(t *testing.T) {
	start := time.Unix(100, 0).UTC()
	trace := newWakePhaseTrace(start)
	trace.admissionStarted = start.Add(10 * time.Millisecond)
	trace.schedulerComplete = start.Add(30 * time.Millisecond)
	trace.targetPublished = start.Add(60 * time.Millisecond)
	trace.proxyStarted = start.Add(100 * time.Millisecond)

	got := trace.gatewayPhasesMS(start.Add(150 * time.Millisecond))
	want := map[string]int64{
		"pre_admission":      10,
		"scheduler_wake":     20,
		"target_publication": 30,
		"post_publication":   40,
		"internal_proxy":     50,
	}
	if len(got) != len(want) {
		t.Fatalf("gateway phases = %#v, want %d phases", got, len(want))
	}
	for phase, wantMS := range want {
		if got[phase] != wantMS {
			t.Errorf("gateway phase %q = %d ms, want %d ms", phase, got[phase], wantMS)
		}
	}
}

type phaseTraceBackend struct{ *fakeBackend }

func (b *phaseTraceBackend) Admit(ctx context.Context, appID, deploymentID, scope, trigger string, maxConcurrency int) (string, WakeMethod, bool, error) {
	markWakeAdmissionStarted(ctx)
	wakeID, method, atCapacity, err := b.fakeBackend.Admit(ctx, appID, deploymentID, scope, trigger, maxConcurrency)
	markWakeSchedulerComplete(ctx)
	markWakeTargetPublished(ctx)
	return wakeID, method, atCapacity, err
}

func TestColdWakeTraceReachesForwardRequest(t *testing.T) {
	original, backend, _ := newTestHandler(t)
	h := NewHandlerWith(&phaseTraceBackend{fakeBackend: backend}, NewMetrics(), original.log)
	forwardSawTrace := false
	h.WithForwarding(func(_ Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			trace := wakePhaseTraceFrom(r.Context())
			if trace == nil {
				t.Error("cold-wake phase trace missing from forward request context")
			} else {
				trace.markProxyStarted(time.Now())
				phases := trace.gatewayPhasesMS(time.Now())
				if len(phases) != 5 {
					t.Errorf("forward request phase count = %d, want 5: %#v", len(phases), phases)
				}
				forwardSawTrace = true
			}
			w.WriteHeader(http.StatusOK)
		})
	})
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !forwardSawTrace {
		t.Fatal("forwarding path did not observe cold-wake phase trace")
	}
}
