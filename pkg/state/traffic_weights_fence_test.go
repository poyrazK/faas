package state

import (
	"context"
	"errors"
	"testing"
)

// A manual traffic change made with the split it was planned from refuses to
// overwrite a split that changed in between (storage review finding #2).
func TestMem_UpdateDeploymentTraffic_ExpectedTrafficWeights(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	_, _, depPrior := memstoreSeedAppLive(t, m, ctx, "weights-fence-mem")
	depPrior, depCanary := memstoreSeedLiveSibling(t, m, ctx, depPrior, "weights-canary-mem")
	if _, err := m.UpdateDeploymentTraffic(ctx, depCanary, 25); err != nil {
		t.Fatalf("split 75/25: %v", err)
	}
	planned := map[string]int{depPrior: 75, depCanary: 25}
	// Another change lands after the rollback was planned.
	if _, err := m.UpdateDeploymentTraffic(ctx, depCanary, 50); err != nil {
		t.Fatalf("split 50/50: %v", err)
	}
	if _, err := m.UpdateDeploymentTraffic(WithExpectedTrafficWeights(ctx, planned), depPrior, 100); !errors.Is(err, ErrTrafficServingChanged) {
		t.Fatalf("stale split: err = %v, want ErrTrafficServingChanged", err)
	}
	if d, _ := m.DeploymentByID(ctx, depCanary); d.TrafficPercent != 50 {
		t.Fatalf("refused change still moved traffic: canary = %d", d.TrafficPercent)
	}
	current := map[string]int{depPrior: 50, depCanary: 50}
	if _, err := m.UpdateDeploymentTraffic(WithExpectedTrafficWeights(ctx, current), depPrior, 100); err != nil {
		t.Fatalf("current split: %v", err)
	}
	if d, _ := m.DeploymentByID(ctx, depPrior); d.TrafficPercent != 100 {
		t.Fatalf("prior = %d, want 100", d.TrafficPercent)
	}
}

func TestCheckExpectedTrafficWeights(t *testing.T) {
	const a, b = "6f1c1b7e-2f0a-4f43-9d2b-0c8a1e5d9a01", "6f1c1b7e-2f0a-4f43-9d2b-0c8a1e5d9a02"
	tests := []struct {
		name     string
		expected map[string]int
		current  map[string]int
		ok       bool
	}{
		{"same split", map[string]int{a: 60, b: 40}, map[string]int{a: 60, b: 40}, true},
		{"zero-weight release ignored", map[string]int{a: 100}, map[string]int{a: 100, b: 0}, true},
		{"id case differs", map[string]int{"6F1C1B7E-2F0A-4F43-9D2B-0C8A1E5D9A01": 100}, map[string]int{a: 100}, true},
		{"weights moved", map[string]int{a: 60, b: 40}, map[string]int{a: 50, b: 50}, false},
		{"release stopped serving", map[string]int{a: 60, b: 40}, map[string]int{a: 100, b: 0}, false},
		{"new release serving", map[string]int{a: 100}, map[string]int{a: 90, b: 10}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkExpectedTrafficWeights(WithExpectedTrafficWeights(context.Background(), tt.expected), tt.current)
			if tt.ok != (err == nil) {
				t.Fatalf("err = %v, want ok=%v", err, tt.ok)
			}
		})
	}
	if err := checkExpectedTrafficWeights(context.Background(), map[string]int{a: 10}); err != nil {
		t.Fatalf("no expectation: %v", err)
	}
}
