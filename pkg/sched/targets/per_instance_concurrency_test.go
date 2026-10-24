// adr: 20261022203523
package targets

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// lockedBuffer is a goroutine-safe log sink.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) count(s string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Count(b.buf.String(), s)
}

const oversubscribedLine = "per-instance concurrency oversubscribed"

// TestTrigger_PerInstanceConcurrencyScalesOnFleetInflight pins the fleet-wide
// half of ADR-20261022203523. Each gateway enforces per_instance_concurrency
// on its own, so two gateways can put 2×N requests on one instance while each
// asks for capacity for only its own share. vmmd's in-flight count sees both
// gateways; the trigger scales on it as an implied concurrent_requests target
// and warns that the instance is oversubscribed.
func TestTrigger_PerInstanceConcurrencyScalesOnFleetInflight(t *testing.T) {
	for _, tc := range []struct {
		name       string
		app        state.App
		inflight   int64
		wantAdmits int
		wantWarn   int
	}{
		{
			name:     "two gateways at N each oversubscribe one instance",
			app:      state.App{ID: "app1", MaxConcurrency: 3, ScalingPolicy: &state.ScalingPolicy{PerInstanceConcurrency: 2}},
			inflight: 4, wantAdmits: 1, wantWarn: 1,
		},
		{
			name:     "one gateway at N is within the declaration",
			app:      state.App{ID: "app1", MaxConcurrency: 3, ScalingPolicy: &state.ScalingPolicy{PerInstanceConcurrency: 2}},
			inflight: 2,
		},
		{
			name: "a declared concurrent_requests target wins",
			app: state.App{ID: "app1", MaxConcurrency: 3, ScalingPolicy: &state.ScalingPolicy{
				PerInstanceConcurrency: 2,
				Targets:                []state.ScalingTarget{{Metric: api.ScalingMetricConcurrentRequests, Value: 10}},
			}},
			inflight: 4, wantWarn: 1,
		},
		{
			name: "workers serve no requests",
			app: state.App{ID: "app1", MaxConcurrency: 3, WorkloadClass: state.WorkloadClassWorker,
				ScalingPolicy: &state.ScalingPolicy{PerInstanceConcurrency: 2}},
			inflight: 4,
		},
		{
			name:     "no declaration keeps today's behaviour",
			app:      state.App{ID: "app1", MaxConcurrency: 3, ScalingPolicy: &state.ScalingPolicy{}},
			inflight: 4,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := &lockedBuffer{}
			engine := &fakeEngine{}
			tr := New(&fakeStore{apps: []state.App{tc.app}},
				&fakeInstats{byApp: map[string]int64{"app1": tc.inflight}},
				engine, &fakeLedger{conc: map[string]int{"app1": 1}},
				Options{Logger: slog.New(slog.NewTextHandler(logs, nil))})
			for range 2 {
				if err := tr.Tick(context.Background()); err != nil {
					t.Fatalf("Tick: %v", err)
				}
			}
			if len(engine.admitCalls) != tc.wantAdmits*2 {
				t.Errorf("admissions over two ticks = %v, want %d per tick", engine.admitCalls, tc.wantAdmits)
			}
			// The warning is rate-limited to one line per app per minute.
			if got := logs.count(oversubscribedLine); got != tc.wantWarn {
				t.Errorf("oversubscription warnings = %d, want %d:\n%s", got, tc.wantWarn, logs.buf.String())
			}
		})
	}
}
