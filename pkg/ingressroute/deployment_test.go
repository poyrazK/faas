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
