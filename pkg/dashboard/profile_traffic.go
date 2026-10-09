package dashboard

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Traffic analysis derives context from the recorded assessment, not live data.
type ProfileTrafficView struct {
	RequestMix                              *ProfileRequestMixView
	BaselineCount, CandidateCount           *int64
	BaselineCoverage, CandidateCoverage     string
	BaselineRate, CandidateRate             *float64
	BaselineCPU, CandidateCPU               *float64
	BaselinePerRequest, CandidatePerRequest *float64
	Status                                  string
	Interpretation                          string
	Warnings                                []string
	Ranked                                  []ProfileTrafficFinding
}

type ProfileTrafficFinding struct {
	Kind, Path, URL               string
	Baseline, Candidate, Increase float64
	Agreement                     string
}

type ProfileCanaryHistoryEntry struct {
	api.CanaryProfileSignal
	Traffic ProfileTrafficView
}

func BuildProfileTraffic(s api.CanaryProfileSignal) ProfileTrafficView {
	o := api.NormalizeProfileRegressionOptions(s.Options)
	v := ProfileTrafficView{Status: "Traffic comparison unavailable"}
	if s.RequestMix != nil {
		mix := ProfileRequestMixSnapshotView(s.RequestMix, o.MinimumRequests)
		v.RequestMix = &mix
	}
	if s.BaselineRequests != nil && *s.BaselineRequests >= 0 {
		v.BaselineCount = s.BaselineRequests
	}
	if s.CandidateRequests != nil && *s.CandidateRequests >= 0 {
		v.CandidateCount = s.CandidateRequests
	}
	coverageSummary := func(c *api.ProfileCoverage) string {
		if c == nil || !c.Available || !finiteTraffic(c.WindowSeconds) || c.WindowSeconds <= 0 || !finiteTraffic(c.CoveredSeconds) {
			return "Unavailable"
		}
		return fmt.Sprintf("%d profiles; %.1f%% recorded coverage; %d recorded upload failures", c.ReceivedProfiles, 100*c.CoveredSeconds/c.WindowSeconds, c.RecordedFailedUploads)
	}
	v.BaselineCoverage = coverageSummary(s.BaselineCoverage)
	v.CandidateCoverage = coverageSummary(s.CandidateCoverage)
	window := func(q *api.ProfileQuery, count *int64, c *api.ProfileCoverage, side string) (*float64, bool) {
		seconds := 0.0
		if q != nil {
			seconds = q.End.Sub(q.Start).Seconds()
		}
		coverageOK := c != nil && c.Available && seconds > 0 && finiteTraffic(c.WindowSeconds) && math.Abs(c.WindowSeconds-seconds) <= 1e-6 && finiteTraffic(c.CoveredSeconds) && c.CoveredSeconds >= 0 && c.CoveredSeconds <= seconds+1e-6 && c.ReceivedProfiles >= o.MinimumProfiles && c.ContributingCollectors > 0 && c.RecordedFailedUploads == 0 && c.CoveredSeconds/seconds >= o.MinimumCoverageRatio
		if !coverageOK {
			v.Warnings = append(v.Warnings, side+" profile coverage is unavailable or below the recorded policy requirements.")
		}
		if count == nil || *count < 0 || seconds <= 0 {
			v.Warnings = append(v.Warnings, side+" request telemetry is unavailable.")
			return nil, false
		}
		rate := float64(*count) / seconds
		if *count < o.MinimumRequests || *count == 0 {
			v.Warnings = append(v.Warnings, fmt.Sprintf("%s traffic is insufficient: %d requests; require at least %d and a positive count.", side, *count, o.MinimumRequests))
			return &rate, false
		}
		return &rate, coverageOK
	}
	var baselineOK, candidateOK bool
	v.BaselineRate, baselineOK = window(s.Baseline, s.BaselineRequests, s.BaselineCoverage, "Stable")
	v.CandidateRate, candidateOK = window(s.Candidate, s.CandidateRequests, s.CandidateCoverage, "Canary")
	if s.Total != nil {
		v.BaselineCPU = &s.Total.BaselineCPUPerSecond
		v.CandidateCPU = &s.Total.CandidateCPUPerSecond
	}
	if !baselineOK || !candidateOK {
		return v
	}
	normalize := func(m api.ProfileRegressionMetric) (float64, float64, bool) {
		a, b := m.BaselineCPUPerSecond / *v.BaselineRate, m.CandidateCPUPerSecond / *v.CandidateRate
		return a, b, finiteTraffic(a) && finiteTraffic(b) && a >= 0 && b >= 0
	}
	if s.Total != nil {
		a, b, ok := normalize(*s.Total)
		if ok {
			v.BaselinePerRequest = &a
			v.CandidatePerRequest = &b
			v.Status = trafficAgreement(s.Total.BaselineCPUPerSecond, s.Total.CandidateCPUPerSecond, a, b, o)
			switch {
			case s.Total.CandidateCPUPerSecond > s.Total.BaselineCPUPerSecond && b <= a && *v.CandidateRate > *v.BaselineRate:
				v.Interpretation = "CPU/s and request rate increased while average CPU/request stayed flat or fell. Higher traffic may explain the CPU increase."
			case s.Total.CandidateCPUPerSecond > s.Total.BaselineCPUPerSecond && b > a:
				v.Interpretation = "CPU/s and average CPU/request both increased. Request volume alone does not explain this pattern; investigate code, background work and traffic mix."
			case b > a:
				v.Interpretation = "Average CPU/request increased even though total CPU/s did not. Lower traffic can hide a per-request increase in infrastructure graphs."
			default:
				v.Interpretation = "Neither total CPU/s nor average CPU/request increased. Individual findings may still differ."
			}
		}
	}
	for i, e := range s.Evidence {
		a, b, ok := normalize(e.Metric)
		if !ok {
			continue
		}
		names := make([]string, 0, len(e.Frames))
		for _, f := range e.Frames {
			names = append(names, f.Name)
		}
		v.Ranked = append(v.Ranked, ProfileTrafficFinding{Kind: e.Kind, Path: strings.Join(names, " → "), URL: s.FindingURL(i), Baseline: a, Candidate: b, Increase: b - a, Agreement: trafficAgreement(e.Metric.BaselineCPUPerSecond, e.Metric.CandidateCPUPerSecond, a, b, o)})
	}
	sort.SliceStable(v.Ranked, func(i, j int) bool { return v.Ranked[i].Increase > v.Ranked[j].Increase })
	return v
}

func finiteTraffic(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }

func trafficAgreement(cpuA, cpuB, requestA, requestB float64, o api.ProfileRegressionOptions) string {
	if cpuA <= 0 || requestA <= 0 {
		return "Threshold agreement unavailable: zero baseline"
	}
	meets := func(value, threshold float64) bool {
		return value >= threshold || threshold-value <= 1e-12*math.Max(math.Abs(value), math.Abs(threshold))
	}
	passes := func(a, b, absolute float64) bool {
		return meets(b-a, absolute) && meets((b/a-1)*100, o.RelativeIncreasePercent)
	}
	cpu, request := passes(cpuA, cpuB, o.AbsoluteIncreaseCPUPerSecond), passes(requestA, requestB, o.AbsoluteIncreaseCPUSecondsPerRequest)
	switch {
	case cpu && request:
		return "Both CPU metrics exceed their thresholds"
	case cpu:
		return "CPU/s exceeds thresholds; CPU/request does not"
	case request:
		return "CPU/request exceeds thresholds; CPU/s does not"
	default:
		return "Neither CPU metric exceeds both thresholds"
	}
}
