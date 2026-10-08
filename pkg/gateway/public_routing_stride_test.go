// adr: 570
package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

func TestPublicRoutingCapturedWeightsRetainBalancedStride(t *testing.T) {
	h := &Handler{}
	app := App{ID: "app", AccountID: "owner"}
	inputs := PublicRoutingInputs{Scope: "production"}
	rows := []DeploymentWeightsRow{{ID: "stable", TrafficPercent: 75}, {ID: "candidate", TrafficPercent: 25}}
	for window := 0; window < 10; window++ {
		counts := map[string]int{}
		for i := 0; i < 100; i++ {
			snapshot := PublicRoutingSnapshot{Weights: append([]DeploymentWeightsRow(nil), rows...)}
			h.selectPublicRoutingDeployment(httptest.NewRequest(http.MethodGet, "http://guest.test/", nil), app, inputs, &snapshot)
			if snapshot.SelectionReason != "weighted" {
				t.Fatalf("selection reason = %q", snapshot.SelectionReason)
			}
			counts[snapshot.SelectedDeploymentID]++
		}
		if counts["stable"] != 75 || counts["candidate"] != 25 {
			t.Fatalf("captured 25%% stride window %d: stable=%d candidate=%d, want75/25", window, counts["stable"], counts["candidate"])
		}
	}
}

func TestPublicRoutingStrideUsesCurrentSnapshotAndIsolatesApps(t *testing.T) {
	h := &Handler{}
	rows := []DeploymentWeightsRow{{ID: "stable", TrafficPercent: 75}, {ID: "candidate", TrafficPercent: 25}}
	counts := [2]map[string]int{{}, {}}
	for i := 0; i < 100; i++ {
		for app := 0; app < 2; app++ {
			picked, ok := h.publicRoutingStride.pick(App{ID: fmt.Sprint(app), AccountID: "owner"}, "production", rows)
			if !ok {
				t.Fatal("verified roster was not selected")
			}
			counts[app][picked]++
		}
	}
	for app, got := range counts {
		if got["stable"] != 75 || got["candidate"] != 25 {
			t.Fatalf("app%d distribution = %v", app, got)
		}
	}
	app := App{ID: "0", AccountID: "owner"}
	for i := 0; i < 10; i++ {
		picked, ok := h.publicRoutingStride.pick(app, "production", []DeploymentWeightsRow{{ID: "candidate", TrafficPercent: 100}})
		if !ok || picked != "candidate" {
			t.Fatalf("cutover used old roster: %s/%v", picked, ok)
		}
	}
	got := map[string]int{}
	for i := 0; i < 100; i++ {
		picked, _ := h.publicRoutingStride.pick(app, "production", rows)
		got[picked]++
	}
	if got["stable"] != 75 || got["candidate"] != 25 {
		t.Fatalf("rollback distribution = %v", got)
	}
}

func TestPublicRoutingStrideConcurrentPicks(t *testing.T) {
	h := &Handler{}
	rows := []DeploymentWeightsRow{{ID: "stable", TrafficPercent: 75}, {ID: "candidate", TrafficPercent: 25}}
	var stable, candidate atomic.Int64
	var wg sync.WaitGroup
	for worker := 0; worker < 10; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				picked, ok := h.publicRoutingStride.pick(App{ID: "app", AccountID: "owner"}, "production", rows)
				if !ok {
					t.Error("verified concurrent pick refused")
					return
				}
				switch picked {
				case "stable":
					stable.Add(1)
				case "candidate":
					candidate.Add(1)
				default:
					t.Errorf("unverified selection %q", picked)
				}
			}
		}()
	}
	wg.Wait()
	if stable.Load() != 750 || candidate.Load() != 250 {
		t.Fatalf("concurrent distribution=%d/%d", stable.Load(), candidate.Load())
	}
}

func TestPublicRoutingStrideCacheIsBounded(t *testing.T) {
	h := &Handler{}
	rows := []DeploymentWeightsRow{{ID: "stable", TrafficPercent: 75}, {ID: "candidate", TrafficPercent: 25}}
	for i := 0; i < RouteCacheCap+100; i++ {
		if _, ok := h.publicRoutingStride.pick(App{ID: fmt.Sprint(i), AccountID: "owner"}, "production", rows); !ok {
			t.Fatal("verified pick refused")
		}
	}
	if got := h.publicRoutingStride.cursors.Len(); got != RouteCacheCap {
		t.Fatalf("cursor entries=%d, cap=%d", got, RouteCacheCap)
	}
}
