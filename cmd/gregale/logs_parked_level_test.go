package main_test

import (
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// A parked app's logs come from the retained archive, which used to ignore
// --level: `gregale logs app --level error` on a scale-to-zero app printed
// every line (ADR-20261110090000). The filter now matches the live stream:
// the intake level when present, else the line heuristic, at or above the floor.
func TestRetainedLineLevelMatchesFollowsLiveStream(t *testing.T) {
	for _, tc := range []struct {
		name, data, level string
		want              bool
	}{
		{"no filter", `{"line":"hello"}`, "", true},
		{"intake level at floor", `{"line":"x","level":"error"}`, "error", true},
		{"intake level above floor", `{"line":"x","level":"error"}`, "warn", true},
		{"intake level below floor", `{"line":"x","level":"info"}`, "warn", false},
		{"heuristic warn", `{"line":"[WARN] disk getting full"}`, "warn", true},
		{"heuristic warn below error", `{"line":"[WARN] disk getting full"}`, "error", false},
		{"unclassified line dropped", `{"line":"listening 3000"}`, "info", false},
		{"level is case-insensitive", `{"line":"x","level":"error"}`, "ERROR", true},
		{"not an envelope", `not json`, "error", false},
	} {
		if got := main.RetainedLineLevelMatches(tc.data, tc.level); got != tc.want {
			t.Errorf("%s: RetainedLineLevelMatches(%s, %q) = %v, want %v", tc.name, tc.data, tc.level, got, tc.want)
		}
	}
}
