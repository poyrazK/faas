package state

import (
	"errors"
	"math"

	"github.com/onebox-faas/faas/pkg/api"
)

func validateProfileAttribution(c *api.ProfileAttributionComparison) error {
	if c == nil {
		return nil
	} // Legacy retained assessment.
	if len(c.Warnings) > api.ProfileAttributionMaxWarnings || !finiteAttribution(c.MaximumChangePercentagePoints) || c.MaximumChangePercentagePoints <= 0 || c.MaximumChangePercentagePoints > 100 {
		return errors.New("invalid attribution comparison")
	}
	for _, warning := range c.Warnings {
		if len(warning) > 1024 {
			return errors.New("attribution warning exceeds storage bounds")
		}
	}
	for _, q := range []*api.ProfileAttributionQuality{c.Baseline, c.Candidate} {
		if err := validateAttributionQuality(q); err != nil {
			return err
		}
	}
	if c.Available {
		if c.Baseline == nil || c.Candidate == nil || !c.Baseline.Available || !c.Candidate.Available || c.DeltaPercentagePoints == nil || !finiteAttribution(*c.DeltaPercentagePoints) {
			return errors.New("attribution comparison is incomplete")
		}
		delta := *c.Candidate.AttributedPercent - *c.Baseline.AttributedPercent
		if math.Abs(delta-*c.DeltaPercentagePoints) > 1e-6 || c.SubstantialChange != (math.Abs(delta) >= c.MaximumChangePercentagePoints) {
			return errors.New("inconsistent attribution change")
		}
	} else if c.SubstantialChange || c.DeltaPercentagePoints != nil {
		return errors.New("unknown attribution comparison has a measured change")
	}
	return nil
}

func validateAttributionQuality(q *api.ProfileAttributionQuality) error {
	if q == nil {
		return nil
	}
	if len(q.Reasons) > api.ProfileAttributionMaxReasons {
		return errors.New("attribution reason summary exceeds storage bounds")
	}
	for _, v := range []float64{q.TotalCPUSeconds, q.AttributedCPUSeconds, q.UnattributedCPUSeconds} {
		if !finiteAttribution(v) || v < 0 {
			return errors.New("invalid attribution CPU")
		}
	}
	tolerance := math.Max(1e-9, q.TotalCPUSeconds*1e-6)
	if math.Abs(q.AttributedCPUSeconds+q.UnattributedCPUSeconds-q.TotalCPUSeconds) > tolerance {
		return errors.New("inconsistent attribution CPU totals")
	}
	if q.Available {
		if q.TotalCPUSeconds <= 0 || q.AttributedPercent == nil || q.UnattributedPercent == nil {
			return errors.New("attribution percentages are incomplete")
		}
		for _, v := range []*float64{q.AttributedPercent, q.UnattributedPercent} {
			if !finiteAttribution(*v) || *v < 0 || *v > 100 {
				return errors.New("invalid attribution percentage")
			}
		}
		if math.Abs(*q.AttributedPercent-100*q.AttributedCPUSeconds/q.TotalCPUSeconds) > 1e-6 || math.Abs(*q.AttributedPercent+*q.UnattributedPercent-100) > 1e-6 {
			return errors.New("inconsistent attribution percentages")
		}
	} else if q.TotalCPUSeconds != 0 || q.AttributedPercent != nil || q.UnattributedPercent != nil {
		return errors.New("unknown attribution contains measured CPU")
	}
	seen := map[string]bool{}
	total := 0.0
	for _, r := range q.Reasons {
		switch r.Reason {
		case "unlabeled", "invalid_label", "route_not_admitted", "encoding_limit", "unknown":
		default:
			return errors.New("invalid attribution reason")
		}
		if seen[r.Reason] || !finiteAttribution(r.CPUSeconds) || r.CPUSeconds < 0 {
			return errors.New("invalid attribution reason CPU")
		}
		seen[r.Reason] = true
		total += r.CPUSeconds
	}
	if total > q.UnattributedCPUSeconds+tolerance {
		return errors.New("attribution reason CPU exceeds unattributed CPU")
	}
	return nil
}

func finiteAttribution(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
