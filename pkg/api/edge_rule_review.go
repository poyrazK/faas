package api

// ADR-20261020105001: log-mode review. A rule's hourly match counts since its last
// change say whether a log-mode rule is ready to enforce and whether any
// rule has gone quiet. This file is the pure part, shared by the dashboard
// and the CLI.

import "time"

// Review statuses.
const (
	// EdgeRuleReviewCollecting: a log-mode rule changed less than
	// EdgeRuleReviewMinObserved ago; there is not enough traffic to judge.
	EdgeRuleReviewCollecting = "collecting"
	// EdgeRuleReviewReady: a log-mode rule observed for at least
	// EdgeRuleReviewMinObserved; its matches show what enforcing would do.
	EdgeRuleReviewReady = "ready"
	// EdgeRuleReviewQuiet: an enabled rule unchanged for the whole quiet
	// window that matched nothing in it.
	EdgeRuleReviewQuiet = "quiet"
)

// Review windows.
const (
	EdgeRuleReviewMinObserved = 24 * time.Hour
	EdgeRuleReviewQuietWindow = 7 * 24 * time.Hour
)

// EdgeRuleReviewInput is one rule and its hourly match totals (matched plus
// logged) starting at SeriesStart, one value per hour. The series should
// cover at least EdgeRuleReviewQuietWindow for the quiet check.
type EdgeRuleReviewInput struct {
	Mode        string
	Enabled     bool
	ChangedAt   time.Time
	SeriesStart time.Time
	Series      []int64
	Now         time.Time
}

// EdgeRuleReview is the outcome for one rule. Status is empty when there is
// nothing to say. Since is where counting started: the hour of the rule's
// last change, or the start of the series when the change is older.
// Matches are counted from Since; PerDay is Matches spread over the
// observed days (at least one).
type EdgeRuleReview struct {
	Status   string
	Since    time.Time
	Observed time.Duration
	Matches  int64
	PerDay   int64
}

// ReviewEdgeRule classifies one rule. Disabled rules are never reviewed;
// an enforced rule is only reported when it has gone quiet.
func ReviewEdgeRule(in EdgeRuleReviewInput) EdgeRuleReview {
	if !in.Enabled {
		return EdgeRuleReview{}
	}
	start := in.SeriesStart.UTC().Truncate(time.Hour)
	since := in.ChangedAt.UTC().Truncate(time.Hour)
	if since.Before(start) {
		since = start
	}
	out := EdgeRuleReview{Since: since, Observed: in.Now.Sub(in.ChangedAt)}
	quietFrom := in.Now.Add(-EdgeRuleReviewQuietWindow).UTC().Truncate(time.Hour)
	var quietMatches int64
	for i, n := range in.Series {
		bucket := start.Add(time.Duration(i) * time.Hour)
		if !bucket.Before(since) {
			out.Matches += n
		}
		if !bucket.Before(quietFrom) {
			quietMatches += n
		}
	}
	days := int64(in.Now.Sub(since) / (24 * time.Hour))
	out.PerDay = out.Matches / max(days, 1)

	observedQuiet := out.Observed >= EdgeRuleReviewQuietWindow && !start.After(quietFrom)
	switch {
	case observedQuiet && quietMatches == 0:
		out.Status = EdgeRuleReviewQuiet
	case in.Mode != EdgeRuleModeLog:
	case out.Observed < EdgeRuleReviewMinObserved:
		out.Status = EdgeRuleReviewCollecting
	default:
		out.Status = EdgeRuleReviewReady
	}
	return out
}

// FoldEdgeRuleSeries folds the last n*size values of an hourly series into
// n sums of size consecutive hours, oldest first; missing leading hours
// count as zero. The dashboard and CLI use it for 7-day trends.
func FoldEdgeRuleSeries(series []int64, size, n int) []int64 {
	out := make([]int64, n)
	offset := len(series) - n*size
	for i, v := range series {
		if i >= offset {
			out[(i-offset)/size] += v
		}
	}
	return out
}
