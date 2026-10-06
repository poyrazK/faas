package routehealth

import (
	"errors"
	"math"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

// ValidateClientErrors binds advisory observations to the already validated
// aggregate window counts, selectors, and deployment/anchor context.
func ValidateClientErrors(report api.RouteHealthReport) error {
	expected := report
	expected.Routes = slices.Clone(report.Routes)
	for i, f := range report.Routes {
		if len(f.WatchStatuses) == 0 {
			if f.ClientErrors != nil {
				return errors.New("unexpected client error evidence")
			}
			continue
		}
		if err := ValidateWatchStatuses(f.WatchStatuses); err != nil {
			return err
		}
		c := f.ClientErrors
		if c == nil || c.MinimumRequests != api.RouteHealthMinRequests || c.MinimumResponses != api.RouteHealthMinErrors || c.RateFloor != api.RouteHealthErrorRateFloor || c.RateDelta != api.RouteHealthErrorRateDelta || c.RateFactor != api.RouteHealthErrorRateFactor || len(c.Statuses) != len(f.WatchStatuses) {
			return errors.New("missing or mismatched watched status evidence")
		}
		seen := map[int]bool{}
		candidateSums, stableSums := make([]int64, len(f.Windows)), make([]int64, len(f.Windows))
		for k, w := range f.Windows {
			if !valid(w.Candidate) || !valid(w.Stable) {
				return errors.New("invalid aggregate counts for watched responses")
			}
			candidateSums[k], stableSums[k] = w.Candidate.ServerErrors, w.Stable.ServerErrors
		}
		clone := *c
		clone.Statuses = slices.Clone(c.Statuses)
		for j, signal := range c.Statuses {
			if !slices.Contains(f.WatchStatuses, signal.StatusCode) || seen[signal.StatusCode] || len(signal.Windows) != len(f.Windows) || len(signal.Windows) != api.RouteHealthWindows {
				return errors.New("invalid watched status inventory")
			}
			seen[signal.StatusCode] = true
			clone.Statuses[j].Windows = slices.Clone(signal.Windows)
			for k, w := range signal.Windows {
				base := f.Windows[k]
				if !w.Start.Equal(base.Start) || !w.End.Equal(base.End) || w.Candidate.Requests != base.Candidate.Requests || w.Stable.Requests != base.Stable.Requests {
					return errors.New("watched status window does not match aggregate evidence")
				}
				for _, counts := range []api.RouteHealthStatusCounts{w.Candidate, w.Stable} {
					want := 0.0
					if counts.Requests > 0 {
						want = float64(counts.Responses) / float64(counts.Requests)
					}
					if counts.Requests < 0 || counts.Responses < 0 || counts.Responses > counts.Requests || math.IsNaN(counts.Rate) || math.IsInf(counts.Rate, 0) || math.Abs(counts.Rate-want) > api.RouteHealthComparisonEpsilon {
						return errors.New("invalid watched status counts or rate")
					}
				}
				if w.Candidate.Responses > base.Candidate.Requests-candidateSums[k] || w.Stable.Responses > base.Stable.Requests-stableSums[k] {
					return errors.New("watched response counts exceed requests")
				}
				candidateSums[k] += w.Candidate.Responses
				stableSums[k] += w.Stable.Responses
			}
		}
		expected.Routes[i].ClientErrors = &clone
	}
	unavailable := ""
	if report.StableDeploymentID == "" {
		unavailable = report.Reason
	}
	EvaluateClientErrors(&expected, unavailable)
	if report.ClientErrorStatus != expected.ClientErrorStatus || report.ClientErrorReason != expected.ClientErrorReason {
		return errors.New("client error summary does not match evidence")
	}
	for i, f := range report.Routes {
		c := f.ClientErrors
		if c == nil {
			continue
		}
		want := expected.Routes[i].ClientErrors
		if c.Status != want.Status || c.Reason != want.Reason {
			return errors.New("client error verdict does not match evidence")
		}
		for j, signal := range c.Statuses {
			if signal.Status != want.Statuses[j].Status || signal.Reason != want.Statuses[j].Reason {
				return errors.New("watched status verdict does not match evidence")
			}
			for k, w := range signal.Windows {
				v := want.Statuses[j].Windows[k]
				if w.Status != v.Status || w.Reason != v.Reason {
					return errors.New("watched status window verdict does not match evidence")
				}
			}
		}
	}
	return nil
}
