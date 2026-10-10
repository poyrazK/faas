package api

import (
	"fmt"
	"slices"
	"time"
)

// Profile kinds. CPU samples on-CPU time; heap samples live allocations
// (ADR-967). Continuous collection defaults to CPU only.
const (
	ProfileKindCPU  = "cpu"
	ProfileKindHeap = "heap"
)

func ValidProfileKind(kind string) bool { return kind == ProfileKindCPU || kind == ProfileKindHeap }

// ProfilingConfig enables sampled CPU profiles (ADR-819). Settings are baked
// into the deployment; changing them requires a redeploy. Kinds adds
// continuous heap profiles (ADR-967); empty means CPU only.
type ProfilingConfig struct {
	Enabled       bool     `json:"enabled" yaml:"enabled" toml:"enabled"`
	WindowSeconds int      `json:"window_seconds,omitempty" yaml:"window_seconds,omitempty" toml:"window_seconds"`
	Kinds         []string `json:"kinds,omitempty" yaml:"kinds,omitempty" toml:"kinds"`
}

// Equal compares two configurations by their effective settings.
func (c *ProfilingConfig) Equal(o *ProfilingConfig) bool {
	if c == nil || o == nil {
		return c == o
	}
	return c.Enabled == o.Enabled && c.WindowSeconds == o.WindowSeconds && slices.Equal(c.EffectiveKinds(), o.EffectiveKinds())
}

// EffectiveKinds returns the continuous profile kinds, defaulting to CPU.
func (c *ProfilingConfig) EffectiveKinds() []string {
	if c == nil || len(c.Kinds) == 0 {
		return []string{ProfileKindCPU}
	}
	return append([]string(nil), c.Kinds...)
}

func (c *ProfilingConfig) EffectiveWindowSeconds() int {
	if c == nil || c.WindowSeconds == 0 {
		return ProfileDefaultWindowSeconds
	}
	return c.WindowSeconds
}

func (c *ProfilingConfig) Validate(plan Plan) error {
	if c == nil {
		return nil
	}
	if c.Enabled && !MustLimitsFor(plan).Profiling.Enabled {
		return fmt.Errorf("CPU profiling is not included on this plan")
	}
	if n := c.EffectiveWindowSeconds(); n < ProfileMinWindowSeconds || n > ProfileMaxWindowSeconds {
		return fmt.Errorf("profiling.window_seconds must be between %d and %d", ProfileMinWindowSeconds, ProfileMaxWindowSeconds)
	}
	seen := map[string]bool{}
	for _, kind := range c.Kinds {
		if !ValidProfileKind(kind) || seen[kind] {
			return fmt.Errorf("profiling.kinds must list distinct values of cpu or heap")
		}
		seen[kind] = true
	}
	return nil
}

type ProfileQuery struct {
	Route        string    `json:"route,omitempty"`
	DeploymentID string    `json:"deployment_id"`
	Start        time.Time `json:"start"`
	End          time.Time `json:"end"`
	Runtime      string    `json:"runtime,omitempty"`
}

type ProfileFunction struct {
	Name            string                 `json:"name"`
	File            string                 `json:"file,omitempty"`
	Line            int64                  `json:"line,omitempty"`
	Source          *ProfileSourceLocation `json:"source,omitempty"`
	SelfCPUSeconds  float64                `json:"self_cpu_seconds"`
	TotalCPUSeconds float64                `json:"total_cpu_seconds"`
}

type ProfileStack struct {
	Name       string                 `json:"name"`
	File       string                 `json:"file,omitempty"`
	Line       int64                  `json:"line,omitempty"`
	Source     *ProfileSourceLocation `json:"source,omitempty"`
	CPUSeconds float64                `json:"cpu_seconds"`
	Children   []*ProfileStack        `json:"children"`
}

type ProfileResponse struct {
	Attribution           *ProfileAttributionQuality `json:"attribution,omitempty"`
	Routes                []ProfileRouteCPU          `json:"routes"`
	RouteRequestsComplete bool                       `json:"route_requests_complete"`
	Query                 ProfileQuery               `json:"query"`
	CPUSeconds            float64                    `json:"cpu_seconds"`
	StackCount            int64                      `json:"stack_count"`
	Functions             []ProfileFunction          `json:"functions"`
	Flamegraph            *ProfileStack              `json:"flamegraph"`
	// Empty distinguishes absence of samples from an observed zero. Profiles
	// cover instrumented processes, not the VM's complete metered CPU usage.
	Empty    bool             `json:"empty"`
	Coverage *ProfileCoverage `json:"coverage,omitempty"`
	Source   *ProfileSource   `json:"source,omitempty"`
}

// ProfileSource is the deployment's recorded Git provenance. It does not
// verify that uploaded or generated files match the recorded commit.
type ProfileSource struct {
	Available  bool   `json:"available"`
	Repository string `json:"repository,omitempty"`
	CommitSHA  string `json:"commit_sha,omitempty"`
	CommitURL  string `json:"commit_url,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// ProfileSourceLocation points to a sampled line at an immutable revision.
type ProfileSourceLocation struct {
	URL  string `json:"url"`
	Path string `json:"path"`
	Line int64  `json:"line"`
}

// ProfileCoverage describes recorded collection, not the proportion of VM
// CPU instrumented. Idle time and losses before ingestion remain unknown.
type ProfileCoverage struct {
	// Private host metadata is projected onto per-route public summaries.
	LabelCounts                map[string]int64           `json:"-"`
	LabelCountsComplete        bool                       `json:"-"`
	LabelCountProfiles         int64                      `json:"-"`
	LabelCountBoundaryProfiles int64                      `json:"-"`
	AttributionReasons         []ProfileAttributionReason `json:"attribution_reasons,omitempty"`
	Available                  bool                       `json:"available"`
	ReceivedProfiles           int64                      `json:"received_profiles"`
	ContributingCollectors     int                        `json:"contributing_collectors"`
	LastReceivedAt             *time.Time                 `json:"last_received_at,omitempty"`
	WindowSeconds              float64                    `json:"window_seconds"`
	CoveredSeconds             float64                    `json:"covered_seconds"`
	GapSeconds                 float64                    `json:"gap_seconds"`
	RecordedFailedUploads      int64                      `json:"recorded_failed_uploads"`
	// Failure records are rate limited; this is a lower bound, not total loss.
	FailuresComplete bool `json:"failures_complete"`
}

type ProfileCompareRequest struct {
	Baseline  ProfileQuery `json:"baseline"`
	Candidate ProfileQuery `json:"candidate"`
}

type ProfileFunctionDelta struct {
	Name                  string                 `json:"name"`
	File                  string                 `json:"file,omitempty"`
	Line                  int64                  `json:"line,omitempty"`
	BaselineSource        *ProfileSourceLocation `json:"baseline_source,omitempty"`
	CandidateSource       *ProfileSourceLocation `json:"candidate_source,omitempty"`
	BaselineCPUPerSecond  float64                `json:"baseline_cpu_per_second"`
	CandidateCPUPerSecond float64                `json:"candidate_cpu_per_second"`
	DeltaCPUPerSecond     float64                `json:"delta_cpu_per_second"`
	BaselineObserved      bool                   `json:"baseline_observed"`
	CandidateObserved     bool                   `json:"candidate_observed"`
	DeltaKnown            bool                   `json:"delta_known"`
}

// ProfileStackDelta compares inclusive CPU rates for a complete caller path.
// An omitted rate means that path was not observed, rather than measured zero.
// WidthCPUPerSecond is the sum of observed rates on both sides, preserving an
// additive layout even when their most expensive children differ.
type ProfileStackDelta struct {
	Name                  string                 `json:"name"`
	File                  string                 `json:"file,omitempty"`
	BaselineLine          int64                  `json:"baseline_line,omitempty"`
	CandidateLine         int64                  `json:"candidate_line,omitempty"`
	BaselineSource        *ProfileSourceLocation `json:"baseline_source,omitempty"`
	CandidateSource       *ProfileSourceLocation `json:"candidate_source,omitempty"`
	BaselineCPUPerSecond  *float64               `json:"baseline_cpu_per_second,omitempty"`
	CandidateCPUPerSecond *float64               `json:"candidate_cpu_per_second,omitempty"`
	DeltaCPUPerSecond     *float64               `json:"delta_cpu_per_second,omitempty"`
	WidthCPUPerSecond     float64                `json:"width_cpu_per_second"`
	Children              []*ProfileStackDelta   `json:"children"`
}

type ProfileCompareResponse struct {
	Attribution      *ProfileAttributionComparison `json:"attribution,omitempty"`
	RouteAdjustment  *ProfileRouteAdjustment       `json:"route_adjustment,omitempty"`
	Baseline         ProfileResponse               `json:"baseline"`
	Candidate        ProfileResponse               `json:"candidate"`
	Functions        []ProfileFunctionDelta        `json:"functions"`
	Comparable       bool                          `json:"comparable"`
	Reason           string                        `json:"reason,omitempty"`
	Flamegraph       *ProfileStackDelta            `json:"flamegraph,omitempty"`
	FlamegraphReason string                        `json:"flamegraph_reason,omitempty"`
}
