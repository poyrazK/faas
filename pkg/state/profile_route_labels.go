package state

import (
	"errors"
	"math"

	"github.com/onebox-faas/faas/pkg/api"
)

func validateRouteLabelComparison(c *api.ProfileRouteLabelComparison) error {
	if c == nil {
		return nil
	} // Historical checks predate request counters.
	if len(c.Reason) > 1024 || !finiteAttribution(c.MinimumPercent) || c.MinimumPercent <= 0 || c.MinimumPercent > 100 || !finiteAttribution(c.MaximumChangePercentagePoints) || c.MaximumChangePercentagePoints <= 0 || c.MaximumChangePercentagePoints > 100 {
		return errors.New("invalid route label comparison")
	}
	for _, q := range []*api.ProfileRouteLabelCoverage{c.Baseline, c.Candidate} {
		if err := validateRouteLabelCoverage(q); err != nil {
			return err
		}
	}
	if !c.Available {
		if c.Consistent || c.DeltaPercentagePoints != nil {
			return errors.New("unknown route label comparison contains measured change")
		}
		return nil
	}
	if c.Baseline == nil || c.Candidate == nil || !c.Baseline.Available || !c.Candidate.Available || c.DeltaPercentagePoints == nil || !finiteAttribution(*c.DeltaPercentagePoints) {
		return errors.New("route label comparison is incomplete")
	}
	delta := *c.Candidate.Percent - *c.Baseline.Percent
	consistent := *c.Baseline.Percent >= c.MinimumPercent && *c.Candidate.Percent >= c.MinimumPercent && math.Abs(delta) < c.MaximumChangePercentagePoints
	if math.Abs(delta-*c.DeltaPercentagePoints) > 1e-6 || c.Consistent != consistent {
		return errors.New("inconsistent route labeling change")
	}
	return nil
}

func validateRouteLabelCoverage(q *api.ProfileRouteLabelCoverage) error {
	if q == nil {
		return nil
	}
	if len(q.Reason) > 1024 || q.CapturedProfiles < 0 || q.BoundaryProfiles < 0 {
		return errors.New("invalid route label coverage")
	}
	for _, count := range []*int64{q.LabeledRequests, q.ObservedRequests} {
		if count != nil && *count < 0 {
			return errors.New("invalid labeled request count")
		}
	}
	if !q.Available {
		if q.Percent != nil {
			return errors.New("unknown route label coverage has a measured share")
		}
		return nil
	}
	if q.Percent == nil || !finiteAttribution(*q.Percent) || q.LabeledRequests == nil || q.ObservedRequests == nil || *q.ObservedRequests <= 0 || q.CapturedProfiles == 0 || *q.LabeledRequests > *q.ObservedRequests {
		return errors.New("route label coverage cannot be reconciled")
	}
	expected := 100 * float64(*q.LabeledRequests) / float64(*q.ObservedRequests)
	if math.Abs(expected-*q.Percent) > 1e-6 {
		return errors.New("inconsistent route labeling share")
	}
	return nil
}
