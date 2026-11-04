// adr: 078
// adr: 069
package fcvm

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func newGuestEventBoundsManager(t *testing.T, inst *Instance) *Manager {
	t.Helper()
	m := NewManager(nil, nil, Paths{}, "test", nil, nil)
	m.mu.Lock()
	m.live["i-guest"] = inst
	m.mu.Unlock()
	return m
}

// elapsed_ms on a tail terminal is guest-controlled. The runner cancels a
// task at the plan's TailTimeoutSeconds, so a larger report is clamped, and a
// near-MaxInt64 report must not wrap the accumulator: meterd writes it to
// usage_minutes.tail_seconds and the fleet-wide usage_daily rollup sums it
// into a bigint.
func TestMarkInstanceTailTerminalBoundsGuestElapsed(t *testing.T) {
	ceiling := int64(api.PlanPro.TailTimeoutSeconds()) + 1
	cases := []struct {
		name    string
		elapsed []int64
		want    int64
	}{
		{"ordinary task rounds up", []int64{1500}, 2},
		{"one over the timeout keeps its grace second", []int64{ceiling*1000 - 500}, ceiling},
		{"huge report clamps to the plan ceiling", []int64{math.MaxInt64}, ceiling},
		{"repeated huge reports stay bounded", []int64{math.MaxInt64, math.MaxInt64, math.MaxInt64}, 3 * ceiling},
		{"negative and zero add nothing", []int64{-5, 0}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newGuestEventBoundsManager(t, &Instance{AppID: "app-1", Plan: api.PlanPro})
			for _, elapsed := range tc.elapsed {
				if _, _, err := m.MarkInstanceTailTerminal(context.Background(), "i-guest", TailOutcomeCompleted, elapsed); err != nil {
					t.Fatal(err)
				}
			}
			got, ok := m.ReadAndResetTailSeconds("i-guest")
			if !ok || got != tc.want {
				t.Fatalf("tail seconds = %d (ok=%v), want %d", got, ok, tc.want)
			}
		})
	}
}

func TestMarkInstanceTailTerminalAccumulatorSaturates(t *testing.T) {
	m := newGuestEventBoundsManager(t, &Instance{AppID: "app-1", Plan: api.PlanScale})
	m.mu.Lock()
	m.live["i-guest"].tailSecondsAccum = maxTailSecondsPerTick - 1
	m.mu.Unlock()
	if _, _, err := m.MarkInstanceTailTerminal(context.Background(), "i-guest", TailOutcomeCompleted, 60_000); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.ReadAndResetTailSeconds("i-guest"); got != maxTailSecondsPerTick {
		t.Fatalf("tail seconds = %d, want saturation at %d", got, maxTailSecondsPerTick)
	}
}

// Sidecar names in guest lifecycle frames become Prometheus label values and
// event fields in root vmmd. Only the sidecars the instance was booted with
// are accepted, so a guest cannot mint unbounded series.
func TestInstanceSidecarOwnerAcceptsOnlyDeclaredSidecars(t *testing.T) {
	m := newGuestEventBoundsManager(t, &Instance{
		AppID: "app-1", DeploymentID: "dep-1",
		WorkloadNames: workloadNamesFor([]WorkloadSpec{{Name: "worker"}, {Name: "migrate"}}),
	})
	for _, name := range []string{"worker", "migrate"} {
		dep, app, err := m.InstanceSidecarOwner("i-guest", name)
		if err != nil || dep != "dep-1" || app != "app-1" {
			t.Fatalf("%s: dep=%q app=%q err=%v", name, dep, app, err)
		}
	}
	for _, name := range []string{"", WorkloadNameMain, "attacker-0001", "worker2"} {
		if _, _, err := m.InstanceSidecarOwner("i-guest", name); !errors.Is(err, ErrUndeclaredSidecar) {
			t.Fatalf("%q: err = %v, want ErrUndeclaredSidecar", name, err)
		}
	}
	if _, _, err := m.InstanceSidecarOwner("i-missing", "worker"); err == nil || errors.Is(err, ErrUndeclaredSidecar) {
		t.Fatalf("unknown instance: err = %v, want a not-live error", err)
	}
}

func TestInstanceSidecarOwnerRejectsEverySidecarWithoutWorkloads(t *testing.T) {
	m := newGuestEventBoundsManager(t, &Instance{AppID: "app-1", DeploymentID: "dep-1"})
	if _, _, err := m.InstanceSidecarOwner("i-guest", "worker"); !errors.Is(err, ErrUndeclaredSidecar) {
		t.Fatalf("err = %v, want ErrUndeclaredSidecar", err)
	}
}
