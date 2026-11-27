package apidserver

import (
	"context"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// pagedFlagEnvironments lists environments by id, as the PgStore does.
type pagedFlagEnvironments struct {
	ids   []string
	calls int
}

func (p *pagedFlagEnvironments) ListFeatureFlagEnvironments(_ context.Context, after string, limit int) ([]state.FeatureFlagAutoRolloutCandidate, error) {
	p.calls++
	var out []state.FeatureFlagAutoRolloutCandidate
	for _, id := range p.ids {
		if id > after && len(out) < limit {
			out = append(out, state.FeatureFlagAutoRolloutCandidate{Scope: state.FeatureFlagScope{EnvironmentID: id}})
		}
	}
	return out, nil
}

// One lifecycle pass reaches every environment, not only the first page:
// apid restarts more often than the pass interval, so a cursor carried
// between passes would never get past the first page.
func TestFeatureFlagLifecyclePassVisitsEveryPage(t *testing.T) {
	for _, n := range []int{0, 1, featureFlagLifecycleBatch, 2*featureFlagLifecycleBatch + 1} {
		lister := &pagedFlagEnvironments{}
		for i := 0; i < n; i++ {
			lister.ids = append(lister.ids, fmt.Sprintf("env-%05d", i))
		}
		seen := map[string]bool{}
		err := forEachFeatureFlagEnvironment(context.Background(), lister, func(env state.FeatureFlagAutoRolloutCandidate) {
			if seen[env.Scope.EnvironmentID] {
				t.Errorf("n=%d: %s visited twice", n, env.Scope.EnvironmentID)
			}
			seen[env.Scope.EnvironmentID] = true
		})
		if err != nil || len(seen) != n {
			t.Fatalf("n=%d: visited %d, err %v", n, len(seen), err)
		}
		if want := n/featureFlagLifecycleBatch + 1; lister.calls != want {
			t.Errorf("n=%d: %d list calls; want %d", n, lister.calls, want)
		}
	}
	// A cancelled pass stops without visiting anything.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	visited := 0
	if err := forEachFeatureFlagEnvironment(ctx, &pagedFlagEnvironments{ids: []string{"env-1"}}, func(state.FeatureFlagAutoRolloutCandidate) { visited++ }); err == nil || visited != 0 {
		t.Fatalf("cancelled pass: err %v, visited %d", err, visited)
	}
}
