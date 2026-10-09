package state

import (
	"encoding/json"
	"errors"
	"io/fs"
	"math"

	"github.com/onebox-faas/faas/pkg/api"
)

func validateProfileRouteChecks(o api.ProfileRegressionOptions, checks []api.ProfileRouteRegression) error {
	if len(o.Routes) > api.ProfileRouteRegressionMaxRoutes {
		return errors.New("too many configured advisory routes")
	}
	if len(checks) > api.ProfileRouteRegressionMaxRoutes || (len(checks) != 0 && len(checks) != len(o.Routes)) {
		return errors.New("route check summary exceeds policy bounds")
	}
	for i, check := range checks {
		if err := validateRouteCodeEvidence(check); err != nil {
			return err
		}
		if err := validateRouteLabelComparison(check.LabelCoverage); err != nil {
			return err
		}
		if check.LabelCoverage != nil && !check.LabelCoverage.Consistent && check.Status != "insufficient_data" {
			return errors.New("inconsistent labeling cannot produce a route conclusion")
		}
		if check.Route != o.Routes[i] || !api.ValidProfileRoute(check.Route) || len(check.Reason) > 1024 || check.ComparisonURL != "" {
			return errors.New("invalid retained route check")
		}
		if check.Status != "regressed" && check.Status != "no_regression_detected" && check.Status != "insufficient_data" {
			return errors.New("invalid route check status")
		}
		for _, count := range []*int64{check.BaselineRequests, check.CandidateRequests} {
			if count != nil && *count < 0 {
				return errors.New("invalid route request count")
			}
		}
		if (check.Status == "insufficient_data") != (check.Metric == nil) {
			return errors.New("route metric does not match status")
		}
		if m := check.Metric; m != nil {
			if m.BaselineCPUSecondsPerRequest <= 0 || m.CandidateCPUSecondsPerRequest <= 0 || m.RelativeIncreasePercent == nil || math.IsNaN(*m.RelativeIncreasePercent) || math.IsInf(*m.RelativeIncreasePercent, 0) || (check.Status == "regressed") != m.ExceedsThreshold {
				return errors.New("invalid comparable route metric")
			}

			for _, value := range []float64{m.BaselineCPUSecondsPerRequest, m.CandidateCPUSecondsPerRequest, m.DeltaCPUSecondsPerRequest} {
				if math.IsNaN(value) || math.IsInf(value, 0) {
					return errors.New("invalid route CPU metric")
				}
			}
		}
	}
	return nil
}

func validateRouteCodeEvidence(check api.ProfileRouteRegression) error {
	body, err := json.Marshal(check.CodeEvidence)
	if err != nil || len(body) > api.ProfileRouteCodeMaxEvidenceBytes+api.ProfileRegressionMaxEvidence+2 || len(check.CodeEvidence) > api.ProfileRegressionMaxEvidence || len(check.CodeReason) > 1024 {
		return errors.New("route code evidence exceeds bounds")
	}
	if check.Status == "insufficient_data" && len(check.CodeEvidence) > 0 {
		return errors.New("insufficient route data cannot attribute code")
	}
	for _, e := range check.CodeEvidence {
		if e.Kind != "function" && e.Kind != "call_path" || len(e.Frames) == 0 || len(e.Frames) > api.ProfileMaxStackDepth+1 || e.Metric.CPUPerRequest == nil {
			return errors.New("invalid route code evidence")
		}
		m := e.Metric.CPUPerRequest
		for _, v := range []float64{e.Metric.BaselineCPUPerSecond, e.Metric.CandidateCPUPerSecond, e.Metric.DeltaCPUPerSecond, m.BaselineCPUSecondsPerRequest, m.CandidateCPUSecondsPerRequest, m.DeltaCPUSecondsPerRequest} {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return errors.New("invalid route code metric")
			}
		}
		if m.BaselineCPUSecondsPerRequest <= 0 || m.CandidateCPUSecondsPerRequest < 0 || m.RelativeIncreasePercent == nil || math.IsNaN(*m.RelativeIncreasePercent) || math.IsInf(*m.RelativeIncreasePercent, 0) || !m.ExceedsThreshold || !e.Metric.ExceedsThreshold {
			return errors.New("invalid route code increase")
		}
		for _, f := range e.Frames {
			if f.BaselineSource != nil || f.CandidateSource != nil || !investigationText(f.Name, api.ProfileMaxSymbolBytes) || !investigationText(f.File, api.ProfileMaxSymbolBytes) || f.Line < 0 || f.BaselineLine < 0 || f.CandidateLine < 0 || f.BaselinePath != "" && !fs.ValidPath(f.BaselinePath) || f.CandidatePath != "" && !fs.ValidPath(f.CandidatePath) {
				return errors.New("invalid retained route source frame")
			}
		}
	}
	return nil
}
