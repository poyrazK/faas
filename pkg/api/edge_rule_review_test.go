package api

import (
	"slices"
	"testing"
	"time"
)

// adr: 20261020105001 — a log-mode rule is collecting for its first day, then ready
// with its matches counted from its last change; an enabled rule with no
// matches through a whole quiet window is quiet; disabled rules and active
// enforced rules are not reported.
func TestReviewEdgeRule(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 30, 0, 0, time.UTC)
	start := now.Add(-EdgeRuleReviewQuietWindow).Truncate(time.Hour)
	hours := int(now.Truncate(time.Hour).Sub(start)/time.Hour) + 1
	series := func(perHour int64, fromHoursAgo int) []int64 {
		s := make([]int64, hours)
		for i := hours - fromHoursAgo - 1; i < hours; i++ {
			if i >= 0 {
				s[i] = perHour
			}
		}
		return s
	}
	cases := []struct {
		name        string
		in          EdgeRuleReviewInput
		status      string
		matches     int64
		perDay      int64
		sinceChange bool
	}{
		{name: "disabled", in: EdgeRuleReviewInput{Mode: EdgeRuleModeLog, ChangedAt: now.Add(-48 * time.Hour), Series: series(5, 10)}},
		{name: "log mode, fresh", in: EdgeRuleReviewInput{Mode: EdgeRuleModeLog, Enabled: true, ChangedAt: now.Add(-3 * time.Hour), Series: series(5, 2)}, status: EdgeRuleReviewCollecting, matches: 15, perDay: 15, sinceChange: true},
		{name: "log mode, counts only since the change", in: EdgeRuleReviewInput{Mode: EdgeRuleModeLog, Enabled: true, ChangedAt: now.Add(-50 * time.Hour), Series: series(2, 100)}, status: EdgeRuleReviewReady, matches: 2 * 51, perDay: 51, sinceChange: true},
		{name: "log mode, ready with nothing matched", in: EdgeRuleReviewInput{Mode: EdgeRuleModeLog, Enabled: true, ChangedAt: now.Add(-30 * time.Hour), Series: series(0, 0)}, status: EdgeRuleReviewReady, sinceChange: true},
		{name: "log mode older than the window", in: EdgeRuleReviewInput{Mode: EdgeRuleModeLog, Enabled: true, ChangedAt: now.Add(-30 * 24 * time.Hour), Series: series(1, 23)}, status: EdgeRuleReviewReady, matches: 24, perDay: 3},
		{name: "enforced and active", in: EdgeRuleReviewInput{Mode: EdgeRuleModeEnforce, Enabled: true, ChangedAt: now.Add(-30 * 24 * time.Hour), Series: series(1, 3)}, matches: 4},
		{name: "enforced and quiet", in: EdgeRuleReviewInput{Mode: EdgeRuleModeEnforce, Enabled: true, ChangedAt: now.Add(-30 * 24 * time.Hour)}, status: EdgeRuleReviewQuiet},
		{name: "log mode and quiet", in: EdgeRuleReviewInput{Mode: EdgeRuleModeLog, Enabled: true, ChangedAt: now.Add(-8 * 24 * time.Hour), Series: series(0, 0)}, status: EdgeRuleReviewQuiet},
		{name: "enforced, quiet but changed this week", in: EdgeRuleReviewInput{Mode: EdgeRuleModeEnforce, Enabled: true, ChangedAt: now.Add(-6 * 24 * time.Hour)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.in.SeriesStart, tc.in.Now = start, now
			got := ReviewEdgeRule(tc.in)
			if got.Status != tc.status || got.Matches != tc.matches || got.PerDay != tc.perDay {
				t.Fatalf("review = %+v, want status %q matches %d per day %d", got, tc.status, tc.matches, tc.perDay)
			}
			if tc.sinceChange && !got.Since.Equal(tc.in.ChangedAt.Truncate(time.Hour)) {
				t.Fatalf("since = %v, want the hour of the change %v", got.Since, tc.in.ChangedAt)
			}
		})
	}
}

// adr: 20261020105001 — folding keeps the newest hours and pads missing old ones.
func TestFoldEdgeRuleSeries(t *testing.T) {
	for _, tc := range []struct {
		series  []int64
		size, n int
		want    []int64
	}{
		{[]int64{1, 2, 3, 4, 5, 6, 7}, 2, 3, []int64{5, 9, 13}},
		{[]int64{1, 2}, 2, 3, []int64{0, 0, 3}},
		{nil, 6, 2, []int64{0, 0}},
		{[]int64{4, 5}, 1, 2, []int64{4, 5}},
	} {
		if got := FoldEdgeRuleSeries(tc.series, tc.size, tc.n); !slices.Equal(got, tc.want) {
			t.Errorf("fold(%v, %d, %d) = %v, want %v", tc.series, tc.size, tc.n, got, tc.want)
		}
	}
}
