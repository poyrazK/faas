package gateway

// spec: §12

import (
	"context"
	"fmt"
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
