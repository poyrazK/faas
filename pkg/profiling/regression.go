package profiling

import (
	"encoding/json"
	"errors"
	"math"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func ValidateRegressionOptions(o api.ProfileRegressionOptions) error {
	o = api.NormalizeProfileRegressionOptions(o)
	if err := validateRegressionRoutes(o.Routes); err != nil {
		return err
	}
	if !finiteCPU(o.RelativeIncreasePercent) || o.RelativeIncreasePercent > api.ProfileRegressionMaxRelativePercent || !finiteCPU(o.AbsoluteIncreaseCPUPerSecond) || o.AbsoluteIncreaseCPUPerSecond <= 0 || o.AbsoluteIncreaseCPUPerSecond > api.ProfileRegressionMaxAbsoluteCPU || o.MinimumProfiles < 1 || o.MinimumProfiles > api.ProfileMaxCoverageEntries || !finiteCPU(o.MinimumCoverageRatio) || o.MinimumCoverageRatio < api.ProfileRegressionMinimumCoverageRatio || o.MinimumCoverageRatio > 1 {
		return errors.New("invalid regression thresholds: require nonnegative percentage, positive CPU/s, 1–5000 profiles and coverage ratio 0.1–1")
	}
	if o.Metric != "cpu_per_second" && o.Metric != "cpu_per_request" {
		return errors.New("metric must be cpu_per_second or cpu_per_request")
	}
	if !finiteCPU(o.AbsoluteIncreaseCPUSecondsPerRequest) || o.AbsoluteIncreaseCPUSecondsPerRequest <= 0 || o.AbsoluteIncreaseCPUSecondsPerRequest > api.ProfileRegressionMaxAbsoluteCPUPerRequest || o.MinimumRequests < 1 || o.MinimumRequests > api.ProfileRegressionMaxMinimumRequests {
		return errors.New("invalid CPU/request thresholds: require positive CPU seconds/request and 1–100000000 minimum requests")
	}
	return nil
}

func NewRegressionAssessment(in api.ProfileInvestigation, o api.ProfileRegressionOptions, now time.Time) api.ProfileRegressionAssessment {
	return api.ProfileRegressionAssessment{InvestigationRevision: in.Revision + 1, CheckedAt: now.UTC(), Status: "inconclusive", Options: api.NormalizeProfileRegressionOptions(o), Baseline: in.Investigation.Baseline, Candidate: in.Investigation.Candidate, Evidence: []api.ProfileRegressionEvidence{}, RouteChecks: unavailableRouteChecks(o, "Profile comparison is unavailable.")}
}

// AssessRegression reports a threshold observation, not deployment causality
// or a statistical test. Unknown observations are never substituted with zero.
func AssessRegression(out api.ProfileRegressionAssessment, a, b api.ProfileResponse) api.ProfileRegressionAssessment {
	return AssessRegressionWithRequests(out, a, b, nil, nil)
}

func AssessRegressionWithRequests(out api.ProfileRegressionAssessment, a, b api.ProfileResponse, baselineRequests, candidateRequests *int64) api.ProfileRegressionAssessment {
	out.Options = api.NormalizeProfileRegressionOptions(out.Options)
	out.Attribution = CompareAttribution(a, b)
	out.RouteChecks = AssessRouteRegressions(out.Options, a, b)
	out.BaselineCoverage, out.CandidateCoverage = a.Coverage, b.Coverage
	out.BaselineRequests, out.CandidateRequests = baselineRequests, candidateRequests
	if err := ValidateRegressionOptions(out.Options); err != nil {
		out.Reason = err.Error()
		return out
	}
	comparison := Compare(a, b)
	if !comparison.Comparable {
		out.Reason = comparison.Reason
		return out
	}
	if !sufficientCoverage(a, out.Options) || !sufficientCoverage(b, out.Options) {
		out.Reason = "Both windows need sufficient recorded profiles and capture coverage without recorded upload failures."
		return out
	}
	if out.Options.Metric == "cpu_per_request" {
		if baselineRequests == nil || candidateRequests == nil {
			out.Reason = "Observed request telemetry is unavailable for one or both deployment windows."
			return out
		}
		if *baselineRequests < out.Options.MinimumRequests || *candidateRequests < out.Options.MinimumRequests {
			out.Reason = "Both deployment windows need the configured minimum number of observed requests."
			return out
		}
	}
	metric, valid := regressionMetric(a.CPUSeconds/a.Query.End.Sub(a.Query.Start).Seconds(), b.CPUSeconds/b.Query.End.Sub(b.Query.Start).Seconds(), out.Options)
	if !valid {
		out.Reason = "Sampled CPU rates are invalid or exceed supported bounds."
		return out
	}
	if out.Options.Metric == "cpu_per_request" {
		metric, valid = addRequestMetric(metric, a.CPUSeconds/float64(*baselineRequests), b.CPUSeconds/float64(*candidateRequests), out.Options)
		if !valid {
			out.Reason = "Observed CPU per request is invalid or exceeds supported bounds."
			return out
		}
	}
	out.Total = &metric
	if positiveIncreaseWithoutRelativeMetric(metric, out.Options) {
		out.UncomparableEntries++
	}
	evidence := []api.ProfileRegressionEvidence{}
	for _, f := range comparison.Functions {
		if !f.DeltaKnown {
			out.UncomparableEntries++
			continue
		}
		m, ok := regressionMetric(f.BaselineCPUPerSecond, f.CandidateCPUPerSecond, out.Options)
		if ok && out.Options.Metric == "cpu_per_request" {
			m, ok = addRequestMetric(m, f.BaselineCPUPerSecond*a.Query.End.Sub(a.Query.Start).Seconds()/float64(*baselineRequests), f.CandidateCPUPerSecond*b.Query.End.Sub(b.Query.Start).Seconds()/float64(*candidateRequests), out.Options)
		}
		if !ok || positiveIncreaseWithoutRelativeMetric(m, out.Options) {
			out.UncomparableEntries++
		} else if m.ExceedsThreshold {
			evidence = append(evidence, api.ProfileRegressionEvidence{Kind: "function", Frames: []api.ProfileCallPathFrame{{
				Name: f.Name, File: f.File, Line: f.Line,
				BaselinePath: profileSourcePath(f.BaselineSource), BaselineLine: profileSourceLine(f.BaselineSource),
				CandidatePath: profileSourcePath(f.CandidateSource), CandidateLine: profileSourceLine(f.CandidateSource),
			}}, Metric: m})
		}
	}
	if comparison.Flamegraph == nil {
		out.UncomparableEntries++
	} else {
		collectRegressionPaths(comparison.Flamegraph, nil, out.Options, a.Query, b.Query, baselineRequests, candidateRequests, &evidence, &out.UncomparableEntries)
	}
	signals := len(evidence) > 0 || metric.ExceedsThreshold
	out.Evidence = boundedRegressionEvidence(evidence)
	switch {
	case signals:
		out.Status = "regressed"
		if out.Options.Metric == "cpu_per_request" {
			out.Reason = "Observed CPU per request increased beyond both configured thresholds. Request telemetry may be incomplete, its minute-bucket timestamps can make boundary counts approximate, and the average is sensitive to traffic mix."
		} else {
			out.Reason = "Observed CPU increases meet both configured thresholds. Traffic, replicas and CPU allocation may also explain the increase."
		}
	case out.UncomparableEntries > 0:
		if out.Options.Metric == "cpu_per_request" {
			out.Reason = "No comparable CPU-per-request signal crossed both thresholds, but some functions or call paths were not observed on both sides or had a zero baseline."
		} else {
			out.Reason = "No comparable signal crossed both thresholds, but some functions or call paths were not observed on both sides or had a zero baseline."
		}
	default:
		out.Status = "no_regression_detected"
		if out.Options.Metric == "cpu_per_request" {
			out.Reason = "No observed total, function or call-path CPU-per-request increase met both thresholds. This is a sampling-based check, not proof of unchanged performance."
		} else {
			out.Reason = "No observed total, function or call-path CPU increase met both thresholds. This is a sampling-based check, not proof of unchanged performance."
		}
	}
	return out
}

func addRequestMetric(m api.ProfileRegressionMetric, baseline, candidate float64, o api.ProfileRegressionOptions) (api.ProfileRegressionMetric, bool) {
	if !finiteCPU(baseline) || !finiteCPU(candidate) || baseline < 0 || candidate < 0 {
		return m, false
	}
	request := &api.ProfileRegressionCPUPerRequestMetric{BaselineCPUSecondsPerRequest: baseline, CandidateCPUSecondsPerRequest: candidate, DeltaCPUSecondsPerRequest: candidate - baseline}
	if baseline > 0 {
		percent := (candidate/baseline - 1) * 100
		if math.IsInf(percent, 0) || math.IsNaN(percent) {
			return m, false
		}
		request.RelativeIncreasePercent = &percent
		request.ExceedsThreshold = meetsRegressionThreshold(request.DeltaCPUSecondsPerRequest, o.AbsoluteIncreaseCPUSecondsPerRequest) && meetsRegressionThreshold(percent, o.RelativeIncreasePercent)
	}
	m.CPUPerRequest = request
	if o.Metric == "cpu_per_request" {
		m.RelativeIncreasePercent = request.RelativeIncreasePercent
		m.ExceedsThreshold = request.ExceedsThreshold
	}
	return m, true
}

func positiveIncreaseWithoutRelativeMetric(m api.ProfileRegressionMetric, o api.ProfileRegressionOptions) bool {
	if o.Metric == "cpu_per_request" {
		return m.CPUPerRequest == nil || m.CPUPerRequest.RelativeIncreasePercent == nil && m.CPUPerRequest.DeltaCPUSecondsPerRequest > 0
	}
	return m.RelativeIncreasePercent == nil && m.DeltaCPUPerSecond > 0
}

func sufficientCoverage(p api.ProfileResponse, o api.ProfileRegressionOptions) bool {
	c := p.Coverage
	seconds := p.Query.End.Sub(p.Query.Start).Seconds()
	return c != nil && c.Available && seconds > 0 && finiteCPU(c.WindowSeconds) && math.Abs(c.WindowSeconds-seconds) <= 1e-6 && finiteCPU(c.CoveredSeconds) && c.CoveredSeconds <= seconds+1e-6 && c.ReceivedProfiles >= o.MinimumProfiles && c.ContributingCollectors > 0 && c.RecordedFailedUploads == 0 && c.CoveredSeconds/seconds >= o.MinimumCoverageRatio
}

func regressionMetric(a, b float64, o api.ProfileRegressionOptions) (api.ProfileRegressionMetric, bool) {
	m := api.ProfileRegressionMetric{BaselineCPUPerSecond: a, CandidateCPUPerSecond: b, DeltaCPUPerSecond: b - a}
	if !finiteCPU(a) || !finiteCPU(b) {
		return m, false
	}
	if a > 0 {
		percent := (b/a - 1) * 100
		if math.IsInf(percent, 0) || math.IsNaN(percent) {
			return m, false
		}
		m.RelativeIncreasePercent = &percent
		m.ExceedsThreshold = meetsRegressionThreshold(m.DeltaCPUPerSecond, o.AbsoluteIncreaseCPUPerSecond) && meetsRegressionThreshold(percent, o.RelativeIncreasePercent)
	}
	return m, true
}

func meetsRegressionThreshold(value, threshold float64) bool {
	return value >= threshold || threshold-value <= 1e-12*math.Max(math.Abs(value), math.Abs(threshold))
}

func collectRegressionPaths(n *api.ProfileStackDelta, parents []api.ProfileCallPathFrame, o api.ProfileRegressionOptions, baseline, candidate api.ProfileQuery, baselineRequests, candidateRequests *int64, evidence *[]api.ProfileRegressionEvidence, unknown *int) {
	frames := append(append([]api.ProfileCallPathFrame(nil), parents...), api.ProfileCallPathFrame{
		Name: n.Name, File: n.File, Line: n.CandidateLine,
		BaselinePath: profileSourcePath(n.BaselineSource), BaselineLine: profileSourceLine(n.BaselineSource),
		CandidatePath: profileSourcePath(n.CandidateSource), CandidateLine: profileSourceLine(n.CandidateSource),
	})
	if len(parents) > 0 {
		if n.BaselineCPUPerSecond == nil || n.CandidateCPUPerSecond == nil {
			*unknown++
		} else if m, ok := regressionMetric(*n.BaselineCPUPerSecond, *n.CandidateCPUPerSecond, o); !ok {
			*unknown++
		} else {
			if o.Metric == "cpu_per_request" {
				m, ok = addRequestMetric(m, *n.BaselineCPUPerSecond*baseline.End.Sub(baseline.Start).Seconds()/float64(*baselineRequests), *n.CandidateCPUPerSecond*candidate.End.Sub(candidate.Start).Seconds()/float64(*candidateRequests), o)
			}
			if !ok || positiveIncreaseWithoutRelativeMetric(m, o) {
				*unknown++
			} else if m.ExceedsThreshold {
				*evidence = append(*evidence, api.ProfileRegressionEvidence{Kind: "call_path", Frames: frames, Metric: m})
			}
		}
	}
	for _, child := range n.Children {
		collectRegressionPaths(child, frames, o, baseline, candidate, baselineRequests, candidateRequests, evidence, unknown)
	}
}

func profileSourcePath(source *api.ProfileSourceLocation) string {
	if source == nil {
		return ""
	}
	return source.Path
}

func profileSourceLine(source *api.ProfileSourceLocation) int64 {
	if source == nil {
		return 0
	}
	return source.Line
}

func boundedRegressionEvidence(in []api.ProfileRegressionEvidence) []api.ProfileRegressionEvidence {
	delta := func(m api.ProfileRegressionMetric) float64 {
		if m.CPUPerRequest != nil {
			return m.CPUPerRequest.DeltaCPUSecondsPerRequest
		}
		return m.DeltaCPUPerSecond
	}
	sort.SliceStable(in, func(i, j int) bool { return delta(in[i].Metric) > delta(in[j].Metric) })
	out, size := []api.ProfileRegressionEvidence{}, 0
	for _, e := range in {
		if len(out) == api.ProfileRegressionMaxEvidence {
			break
		}
		body, err := json.Marshal(e)
		if err != nil || size+len(body) > api.ProfileRegressionMaxEvidenceBytes {
			continue
		}
		size += len(body)
		out = append(out, e)
	}
	return out
}
