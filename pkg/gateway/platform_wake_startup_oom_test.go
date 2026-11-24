package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
)

// startupOOMBackend reports, like the schedd client does for a response
// carrying startup_oom, that the app ran out of memory while an instance this
// admission booted came up. oomInstance "" marks the admitted instance itself;
// any other value marks a sibling the request is not routed to.
type startupOOMBackend struct {
	*fakeBackend
	oom         bool
	oomInstance string
}

func (b *startupOOMBackend) Admit(ctx context.Context, app, deployment, scope, trigger string, maximum int) (string, WakeMethod, bool, error) {
	wakeID, method, atCapacity, err := b.fakeBackend.Admit(ctx, app, deployment, scope, trigger, maximum)
	if err == nil && !atCapacity && b.oom {
		instance := b.oomInstance
		if instance == "" {
			b.mu.Lock()
			instance = b.targets[len(b.targets)-1].InstanceID
			b.mu.Unlock()
		}
		sched.NoteStartupOOM(ctx, instance)
	}
	return wakeID, method, atCapacity, err
}

// metricLineValue returns the value of the exposition line that starts with
// prefix, failing when no such line exists so a renamed or unregistered
// series cannot pass a zero assertion.
func metricLineValue(t *testing.T, m *Metrics, prefix string) int {
	t.Helper()
	for _, line := range strings.Split(bodyForHistogram(t, m), "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(line, prefix)), "%d", &n); err != nil {
			t.Fatalf("parse %q: %v", line, err)
		}
		return n
	}
	t.Fatalf("metric line %q missing", prefix)
	return 0
}

// TestPlatformWakeExcludesStartupOOM: a wake whose app the kernel OOM-killed
// while it booted (2026-11-23: 4 gunicorn workers in a 256 MB VM held every
// cold boot until the kill, 2.9-4.9 s) is the app's latency. It stays out of
// the platform wake SLO histogram and its phase breakdown, is counted as
// excluded, and still lands in the app's own wake latency — also when the
// final pick routes the request to an instance other than the OOM-killed one.
func TestPlatformWakeExcludesStartupOOM(t *testing.T) {
	for _, tc := range []struct {
		name               string
		oom                bool
		oomInstance        string
		platform, excluded int
	}{
		{name: "startup oom", oom: true, platform: 0, excluded: 1},
		{name: "startup oom on a sibling", oom: true, oomInstance: "sibling", platform: 0, excluded: 1},
		{name: "healthy wake", oom: false, platform: 1, excluded: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, fixture, _ := newTestHandler(t)
			h.backend = &startupOOMBackend{fakeBackend: fixture, oom: tc.oom, oomInstance: tc.oomInstance}
			h.WithForwarding(func(Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
			})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+fixture.host+"/", nil))
			if rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
			}
			for prefix, want := range map[string]int{
				"gateway_platform_wake_latency_seconds_count ":                tc.platform,
				`gateway_platform_wake_excluded_total{reason="startup_oom"} `: tc.excluded,
				"gateway_wake_latency_seconds_count ":                         1,
			} {
				if got := metricLineValue(t, h.metrics, prefix); got != want {
					t.Errorf("%s= %d, want %d", prefix, got, want)
				}
			}
		})
	}
}
