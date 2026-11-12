// adr: 20261105120000
package ingressroute

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestServingDeploymentWeights(t *testing.T) {
	rows := []state.Deployment{
		{ID: "stable", AppID: "app", Status: state.DeployLive, TrafficPercent: 80},
		{ID: "canary", AppID: "app", Status: state.DeployLive, TrafficPercent: 20},
		{ID: "zero", AppID: "app", Status: state.DeployLive},
		{ID: "superseded", AppID: "app", Status: state.DeploySuperseded, TrafficPercent: 100},
		{ID: "foreign", AppID: "other", Status: state.DeployLive, TrafficPercent: 100},
	}
	counts := map[string]int{}
	for slot := range 100 {
		id, err := Select(rows, "app", uint64(slot))
		if err != nil {
			t.Fatal(err)
		}
		counts[id]++
	}
	if counts["stable"] != 80 || counts["canary"] != 20 || len(counts) != 2 {
		t.Fatalf("counts=%v", counts)
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	for slot := range 100 {
		id, err := Select(rows, "app", uint64(slot))
		if err != nil {
			t.Fatal(err)
		}
		want := "stable"
		if slot < 20 {
			want = "canary"
		}
		if id != want {
			t.Fatalf("slot=%d id=%s want=%s", slot, id, want)
		}
	}
}

func TestServingDeploymentRejectsInvalidWeights(t *testing.T) {
	for _, weights := range [][]int{nil, {0}, {-1}, {101}, {50}, {80, 80}} {
		rows := []state.Deployment{}
		for i, weight := range weights {
			rows = append(rows, state.Deployment{ID: string(rune('a' + i)), AppID: "app", Status: state.DeployLive, TrafficPercent: weight})
		}
		if id, err := Select(rows, "app", 0); err == nil || id != "" {
			t.Fatalf("weights=%v id=%s err=%v", weights, id, err)
		}
	}
	duplicate := []state.Deployment{{ID: "same", AppID: "app", Status: state.DeployLive, TrafficPercent: 50}, {ID: "same", AppID: "app", Status: state.DeployLive, TrafficPercent: 50}}
	if _, err := Select(duplicate, "app", 0); err == nil {
		t.Fatal("duplicate deployment accepted")
	}
}

type failingDeploymentSource struct{ err error }

func (s failingDeploymentSource) LiveDeployments(context.Context, string) ([]state.Deployment, error) {
	return []state.Deployment{{ID: "live", AppID: "app", Status: state.DeployLive, TrafficPercent: 100}}, s.err
}
func TestServingDeploymentReadFailure(t *testing.T) {
	failure := errors.New("storage unavailable")
	if id, err := Deployment(context.Background(), failingDeploymentSource{err: failure}, "app", 0); id != "" || !errors.Is(err, failure) {
		t.Fatalf("id=%s err=%v", id, err)
	}
}

type fixedDeploymentSource []state.Deployment

func (s fixedDeploymentSource) LiveDeployments(context.Context, string) ([]state.Deployment, error) {
	return s, nil
}
func TestServingDeploymentQuarantineRecheck(t *testing.T) {
	rows := fixedDeploymentSource{
		{ID: "old", AppID: "app", Status: state.DeployLive, TrafficPercent: 0},
		{ID: "current", AppID: "app", Status: state.DeployLive, TrafficPercent: 100},
	}
	if err := RequireDeployment(t.Context(), rows, "app", "old"); err != nil {
		t.Fatal("ordinary zeroed bucket should remain pinned", err)
	}
	rows[0].ParkedReason = string(state.ParkReasonSecurityScanRegressed)
	if err := RequireDeployment(t.Context(), rows, "app", "old"); err == nil {
		t.Fatal("selected quarantined zero-traffic bucket admitted")
	}
	if err := RequireDeployment(t.Context(), rows, "app", "current"); err != nil {
		t.Fatal("unselected retained bucket blocked clean traffic", err)
	}
	rows[0].TrafficPercent = 20
	rows[1].TrafficPercent = 80
	if _, err := Select(rows, "app", 90); err == nil {
		t.Fatal("quarantined canary admitted through clean bucket")
	}
}

func TestRawProductionLaneSelectionAndRecheck(t *testing.T) {
	rows := fixedDeploymentSource{
		{ID: "legacy", AppID: "app", Status: state.DeployLive, TrafficPercent: 100, Scope: "default"},
		{ID: "production", AppID: "app", Status: state.DeployLive, Scope: "production"},
		{ID: "stage", AppID: "app", Status: state.DeployLive, TrafficPercent: 100, Scope: "staging", ParkedReason: string(state.ParkReasonSecurityScanRegressed)},
		{ID: "foreign", AppID: "other", Status: state.DeployLive, TrafficPercent: 100, Scope: "production"},
		{ID: "superseded", AppID: "app", Status: state.DeploySuperseded, TrafficPercent: 100, Scope: "production"},
	}
	if got, err := Select(rows, "app", 0); err != nil || got != "legacy" {
		t.Fatalf("dark production displaced legacy: %s/%v", got, err)
	}
	if err := RequireDeployment(t.Context(), rows, "app", "legacy"); err != nil {
		t.Fatal(err)
	}
	rows[1].TrafficPercent = 100
	if got, err := Select(rows, "app", 0); err != nil || got != "production" {
		t.Fatalf("production did not win: %s/%v", got, err)
	}
	if err := RequireDeployment(t.Context(), rows, "app", "legacy"); err == nil {
		t.Fatal("late lane switch retained legacy target")
	}
	rows[0].ParkedReason = string(state.ParkReasonSecurityScanRegressed)
	if err := RequireDeployment(t.Context(), rows, "app", "production"); err != nil {
		t.Fatal("inactive legacy quarantine blocked production", err)
	}
	if err := RequireDeployment(t.Context(), rows, "app", "stage"); err == nil {
		t.Fatal("staging target admitted")
	}
}
