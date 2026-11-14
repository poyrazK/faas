package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// TestWorkerNoInstancesLineNamesParkedPool pins Hunt 32 H32-32: a parked
// worker with 4 queued messages read "scaled to zero / parked" and never
// said it waits for a wake.
func TestWorkerNoInstancesLineNamesParkedPool(t *testing.T) {
	for _, tc := range []struct {
		status  string
		backlog int64
		want    string
	}{
		{"evicted_cold", 4, "4 queued messages wait until `gregale app wake h32-wk`"},
		{"evicted_cold", 0, "autoscaling resumes after `gregale app wake h32-wk`"},
		{"active", 0, "scaled to zero"},
	} {
		if got := main.WorkerNoInstancesLine("h32-wk", tc.status, tc.backlog); !strings.Contains(got, tc.want) {
			t.Errorf("%s/%d: %q, want it to contain %q", tc.status, tc.backlog, got, tc.want)
		}
	}
}
