package api

import (
	"encoding/json"
	"errors"
	"time"
)

// Both CPU thresholds must be met. Coverage is recorded capture time, not
// instrumentation completeness or statistical confidence.
type ProfileRegressionOptions struct {
	Routes                               []string `json:"routes,omitempty"`
	Metric                               string   `json:"metric,omitempty"`
	RelativeIncreasePercent              float64  `json:"relative_increase_percent"`
	AbsoluteIncreaseCPUPerSecond         float64  `json:"absolute_increase_cpu_per_second"`
	AbsoluteIncreaseCPUSecondsPerRequest float64  `json:"absolute_increase_cpu_seconds_per_request,omitempty"`
	MinimumProfiles                      int64    `json:"minimum_profiles"`
	MinimumCoverageRatio                 float64  `json:"minimum_coverage_ratio"`
	MinimumRequests                      int64    `json:"minimum_requests,omitempty"`
}

func (o *ProfileRegressionOptions) UnmarshalJSON(body []byte) error {
	type options ProfileRegressionOptions
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return err
	}
	for _, name := range []string{"relative_increase_percent", "absolute_increase_cpu_per_second", "minimum_profiles", "minimum_coverage_ratio"} {
		value, ok := fields[name]
		if !ok || string(value) == "null" {
			return errors.New("explicit regression options require all four threshold fields")
		}
	}
	if err := json.Unmarshal(body, (*options)(o)); err != nil {
		return err
	}
	if o.Metric == "cpu_per_request" {
		for _, name := range []string{"absolute_increase_cpu_seconds_per_request", "minimum_requests"} {
			value, ok := fields[name]
			if !ok || string(value) == "null" {
				return errors.New("CPU-per-request checks require an absolute CPU/request threshold and minimum request count")
			}
		}
	}
	return nil
}

func DefaultProfileRegressionOptions() ProfileRegressionOptions {
	return ProfileRegressionOptions{Metric: "cpu_per_second", RelativeIncreasePercent: ProfileRegressionDefaultRelativePercent, AbsoluteIncreaseCPUPerSecond: ProfileRegressionDefaultAbsoluteCPU, AbsoluteIncreaseCPUSecondsPerRequest: ProfileRegressionDefaultAbsoluteCPUPerRequest, MinimumProfiles: ProfileRegressionDefaultMinimumProfiles, MinimumCoverageRatio: ProfileRegressionDefaultCoverageRatio, MinimumRequests: ProfileRegressionDefaultMinimumRequests}
}

func NormalizeProfileRegressionOptions(o ProfileRegressionOptions) ProfileRegressionOptions {
	o.Routes = append([]string(nil), o.Routes...)
	defaults := DefaultProfileRegressionOptions()
	if o.Metric == "" {
		o.Metric = defaults.Metric
	}
	if o.Metric == "cpu_per_second" && o.AbsoluteIncreaseCPUSecondsPerRequest == 0 {
		o.AbsoluteIncreaseCPUSecondsPerRequest = defaults.AbsoluteIncreaseCPUSecondsPerRequest
	}
	if o.Metric == "cpu_per_second" && o.MinimumRequests == 0 {
		o.MinimumRequests = defaults.MinimumRequests
	}
	return o
}

type CheckProfileRegressionRequest struct {
	ExpectedRevision *int64                    `json:"expected_revision"`
	Options          *ProfileRegressionOptions `json:"options,omitempty"`
}

type ProfileRegressionMetric struct {
	BaselineCPUPerSecond    float64                               `json:"baseline_cpu_per_second"`
	CandidateCPUPerSecond   float64                               `json:"candidate_cpu_per_second"`
	DeltaCPUPerSecond       float64                               `json:"delta_cpu_per_second"`
	RelativeIncreasePercent *float64                              `json:"relative_increase_percent,omitempty"`
	CPUPerRequest           *ProfileRegressionCPUPerRequestMetric `json:"cpu_per_request,omitempty"`
	ExceedsThreshold        bool                                  `json:"exceeds_threshold"`
}

type ProfileRegressionCPUPerRequestMetric struct {
	BaselineCPUSecondsPerRequest  float64  `json:"baseline_cpu_seconds_per_request"`
	CandidateCPUSecondsPerRequest float64  `json:"candidate_cpu_seconds_per_request"`
	DeltaCPUSecondsPerRequest     float64  `json:"delta_cpu_seconds_per_request"`
	RelativeIncreasePercent       *float64 `json:"relative_increase_percent,omitempty"`
	ExceedsThreshold              bool     `json:"exceeds_threshold"`
}

type ProfileRegressionEvidence struct {
	Kind   string                  `json:"kind"`
	Frames []ProfileCallPathFrame  `json:"frames"`
	Metric ProfileRegressionMetric `json:"metric"`
}

// A bounded historical summary; raw profiles and source URLs are not stored.
// InvestigationRevision names the revision returned by the successful check.
type ProfileRegressionAssessment struct {
	Attribution           *ProfileAttributionComparison `json:"attribution,omitempty"`
	RouteChecks           []ProfileRouteRegression      `json:"route_checks,omitempty"`
	RequestMix            *ProfileRequestMixSnapshot    `json:"request_mix,omitempty"`
	InvestigationRevision int64                         `json:"investigation_revision"`
	CheckedAt             time.Time                     `json:"checked_at"`
	Status                string                        `json:"status"`
	Reason                string                        `json:"reason"`
	Options               ProfileRegressionOptions      `json:"options"`
	Baseline              ProfileQuery                  `json:"baseline"`
	Candidate             ProfileQuery                  `json:"candidate"`
	BaselineRequests      *int64                        `json:"baseline_requests,omitempty"`
	CandidateRequests     *int64                        `json:"candidate_requests,omitempty"`
	BaselineCoverage      *ProfileCoverage              `json:"baseline_coverage,omitempty"`
	CandidateCoverage     *ProfileCoverage              `json:"candidate_coverage,omitempty"`
	Total                 *ProfileRegressionMetric      `json:"total,omitempty"`
	Evidence              []ProfileRegressionEvidence   `json:"evidence"`
	UncomparableEntries   int                           `json:"uncomparable_entries"`
}
