package gateway

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestPlacementReadinessLoadsOncePerBatch pins the placement repair's
// readiness read: one load for every app the batch holds, never more than the
// loader's target cap in one load, an app never split across loads, and a
// failed load leaving each of its apps without readiness.
func TestPlacementReadinessLoadsOncePerBatch(t *testing.T) {
	targetsFor := func(app string, n int) []Target {
		out := make([]Target, n)
		for i := range out {
			out[i] = placementFixture(app, fmt.Sprintf("%s-%d", app, i))
		}
		return out
	}
	cases := []struct {
		name      string
		sizes     map[string]int
		fail      bool
		wantLoads int
	}{
		{"small apps share one load", map[string]int{"a": 2, "b": 3, "c": 1}, false, 1},
		{"a full batch starts a new load", map[string]int{"a": 100, "b": 100}, false, 2},
		{"failed load leaves every app unready", map[string]int{"a": 2, "b": 2}, true, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			apps := make([]string, 0, len(tc.sizes))
			snapshots := map[string]TargetPlacementSnapshot{}
			for _, app := range []string{"a", "b", "c"} {
				if n, ok := tc.sizes[app]; ok {
					apps = append(apps, app)
					snapshots[app] = placementSnapshot(app, targetsFor(app, n)...)
				}
			}
			var loads [][]Target
			b := NewPGBackend(nil, nil, nil).WithTargetPlacementLoader(func(context.Context, []string) (map[string]TargetPlacementSnapshot, error) {
				return snapshots, nil
			}).WithTargetReadinessLoader(func(_ context.Context, targets []Target) (map[string]TargetReadinessSnapshot, error) {
				loads = append(loads, targets)
				if len(targets) > api.TrafficReadinessBatchSize {
					return nil, fmt.Errorf("readiness batch exceeds %d targets", api.TrafficReadinessBatchSize)
				}
				if tc.fail {
					return nil, errors.New("database unavailable")
				}
				out := make(map[string]TargetReadinessSnapshot, len(targets))
				for _, target := range targets {
					out[target.InstanceID] = TargetReadinessSnapshot{AppID: target.AppID, InstanceID: target.InstanceID}
				}
				return out, nil
			})
			loaded, placementErr, readinessErr := b.loadPlacementSnapshots(t.Context(), apps)
			if placementErr != nil {
				t.Fatalf("placement error: %v", placementErr)
			}
			if len(loads) != tc.wantLoads {
				t.Fatalf("readiness loads = %d, want %d", len(loads), tc.wantLoads)
			}
			seenIn := map[string]int{}
			for i, load := range loads {
				for _, target := range load {
					if prev, ok := seenIn[target.AppID]; ok && prev != i {
						t.Fatalf("app %s split across loads %d and %d", target.AppID, prev, i)
					}
					seenIn[target.AppID] = i
				}
			}
			if tc.fail != (readinessErr != nil) {
				t.Fatalf("readiness error = %v, want failure %v", readinessErr, tc.fail)
			}
			for _, app := range apps {
				snapshot := loaded[app]
				if tc.fail {
					if snapshot.readiness != nil {
						t.Fatalf("app %s kept readiness from a failed load", app)
					}
					continue
				}
				for _, target := range snapshot.Targets {
					if got := snapshot.readiness[target.InstanceID]; got.InstanceID != target.InstanceID || got.AppID != app {
						t.Fatalf("app %s instance %s readiness = %+v", app, target.InstanceID, got)
					}
				}
			}
		})
	}
}
