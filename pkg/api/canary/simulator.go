// Package canary — simulator.go (SAFE-RELEASES-OBS PR-E, issue #976 /
// ADR-122). Read-only canary-safety projection for the `gregale canary
// simulate` CLI subcommand. The simulator pulls the last hour of the
// app's invocations, derives an error rate + p95 latency, and projects
// per-step success probability for a candidate canary ladder. It does
// NOT touch the database (read-only) and does NOT mutate any
// deployment row — purely advisory.
//
// Why a separate file (and not folded into preset.go): the catalog is
// the closed-set of allowed preset shapes; the simulator is a thin
// consumer of those shapes. Keeping the projection logic out of
// preset.go lets a future PR add a `--canary-preset` write path that
// consults the simulator before staging, without bloating the catalog
// file.
//
// Why read-only in PR-E: the plan flags this as "standalone
// operator/CI feature; could be deferred to a follow-up PR if scope
// pressure". Shipping read-only first lets us validate the
// projection math against production traffic before deciding whether
// to wire the output into the deployment's canary_stages column.

package canary

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

// InvocationSample is the minimal projection of an invocation row
// the simulator needs. Defined in pkg/api/canary (not pkg/state) so
// this package stays free of pkg/state imports — pkg/state imports
// pkg/api, so the inverse direction would create a cycle. Callers
// (pkg/state.Store.RecentInvocations + the production adapter in
// cmd/gregale) project Invocation → InvocationSample at the seam.
type InvocationSample struct {
	CreatedAt   time.Time
	CompletedAt time.Time
	Failed      bool
}

// SimulatorSource is the read-side seam the simulator pulls from.
// pkg/state.Store is the production impl; tests pass a fake directly.
// The interface is intentionally narrow — just the two aggregate
// reads (error rate + p95 latency). Recent invocation rows are NOT
// in the interface; callers (the CLI, tests) pass them via
// WithSamples, which keeps the production Store surface
// invocation-typed (pkg/state.Invocation) while the simulator sees
// the minimal projection (InvocationSample). This avoids the
// pkg/state ↔ pkg/api/canary import cycle that the inverse direction
// would create.
type SimulatorSource interface {
	RecentErrorRate(ctx context.Context, appID string, since time.Time) (float64, error)
	RecentP95LatencyMs(ctx context.Context, appID string, since time.Time) (float64, error)
}

// SimulatorOption configures SimulateCanary. The WithAggregate option
// lets callers (mainly tests) skip the source seam entirely by
// pre-supplying the error rate + p95 latency.
type SimulatorOption func(*simulatorConfig)

type simulatorConfig struct {
	errRate         float64
	p95Ms           float64
	traffic         int64
	override        bool
	trafficOverride bool
}

// WithAggregate pre-populates the simulator's aggregate reads (error
// rate + p95 latency). Used by tests; the production CLI supplies
// these via the SimulatorSource seam. When set, the simulator skips
// the source seam entirely (source may be nil).
func WithAggregate(errRate, p95Ms float64) SimulatorOption {
	return func(c *simulatorConfig) {
		c.errRate = errRate
		c.p95Ms = p95Ms
		c.override = true
	}
}

// WithObservedTraffic overrides the number of invocations used to derive
// traffic-per-second and the insufficient-sample guard. The CLI uses the
// one-hour request count from the app metrics endpoint instead of allocating
// one InvocationSample per request, which keeps the read-only simulation safe
// for high-volume apps.
func WithObservedTraffic(count int64) SimulatorOption {
	return func(c *simulatorConfig) {
		if count < 0 {
			count = 0
		}
		c.traffic = count
		c.trafficOverride = true
	}
}

// SimReport is the wire shape of the simulator's output. JSON-friendly
// so the CLI can `--format=json` for CI consumption. Numeric fields
// are clamped to [0,1] for projected_success_p / per_step.success_p;
// durations are seconds as float64 (no sub-second precision needed for
// canary-step decision-making).
type SimReport struct {
	Preset           string         `json:"preset"`
	Stages           int            `json:"stages"`
	ProjectedSuccess float64        `json:"projected_success_p"`
	PerStep          []SimStepEntry `json:"per_step"`
	// ObservedTraffic is the sampled volume the projection is
	// calibrated against. The CLI surfaces this so an operator
	// can sanity-check "we're projecting off N=3 invocations"
	// before trusting the success probability.
	ObservedTraffic int       `json:"observed_traffic"`
	ObservedError   float64   `json:"observed_error_rate"`
	ObservedP95Ms   float64   `json:"observed_p95_latency_ms"`
	WindowStart     time.Time `json:"window_start"`
	WindowEnd       time.Time `json:"window_end"`
	// Note is a free-form advisory line ("warning: low traffic
	// sample — projection has wide CI") that the CLI prints
	// before the table. Empty when no advisory applies.
	Note string `json:"note,omitempty"`
}

// SimStepEntry is one row of the per-step table. Step is 0-indexed
// (mirrors the orchestrator's canary_step_started_at walker). Percent
// is the traffic slice at this stage. Duration is the stage's
// wait-before-advance interval. SuccessP is the projected probability
// that all invocations in this step's window complete without
// crossing the error threshold (see simulateStep's math).
type SimStepEntry struct {
	Step     int     `json:"step"`
	Percent  int     `json:"percent"`
	Duration string  `json:"duration"`
	SuccessP float64 `json:"success_p"`
}

// ErrSimulatorNoSource is returned when the simulator is invoked
// without a wired source. Mirrors the safedeploy.ErrActionDispatcherNoAPID
// pattern so the CLI's error-mapping stays consistent.
var ErrSimulatorNoSource = errors.New("canary: simulator invoked with nil source")

// ErrSimulatorUnknownPreset is returned when --canary-preset is not
// in AllowedCanaryPresets. The CLI checks the closed-set before
// reaching the simulator (mirrors the apid validator at
// cmd/apid/handlers_ext.go), but a programmatic caller bypassing
// the CLI gets the same 422.
var ErrSimulatorUnknownPreset = errors.New("canary: simulator unknown preset")

// ErrSimulatorEmptyStages is returned when the resolved Preset has
// zero stages. "none" preset has zero stages (it's the no-canary
// deployment shape), so the CLI treats this as a fast-path rather
// than an error.
var ErrSimulatorEmptyStages = errors.New("canary: simulator preset has no stages")

// simulateStep projects the per-step success probability. Math:
//
//	per_step_window_seconds = Duration.Seconds()
//	projected_requests      = observed_traffic_rate * per_step_window_seconds
//	                         * (Percent / 100)
//	projected_errors        = projected_requests * observed_error_rate
//	success_p = (1 - observed_error_rate) ^ projected_requests
//
// We use a per-step probability rather than a fleet-wide probability
// because the canary walker flips rollout_state to 'complete' on the
// FIRST step's health-probe failure — a step that runs at 1% traffic
// has very few requests and thus very low power to detect a
// regression. The CLI surfaces this as "step 0's success_p is high
// even at high observed_error_rate" so operators understand why a
// preset that front-loads traffic (aggressive) catches regressions
// faster than a preset that ramps slowly (slow).
//
// NaN / Inf guards: when projected_requests == 0 (no traffic or
// Percent=0), we return 1.0 — there's literally nothing to fail. When
// observed_error_rate > 1.0 (impossible but defensively), we clamp
// to 1.0 so the exponent is non-positive.
func simulateStep(observedTrafficPerSec, observedErrorRate float64, percent int, duration time.Duration) float64 {
	if percent <= 0 || duration <= 0 {
		return 1.0
	}
	if observedErrorRate <= 0 {
		return 1.0
	}
	if observedErrorRate > 1.0 {
		observedErrorRate = 1.0
	}
	projectedRequests := observedTrafficPerSec * duration.Seconds() * (float64(percent) / 100.0)
	if projectedRequests <= 0 {
		return 1.0
	}
	return math.Pow(1.0-observedErrorRate, projectedRequests)
}

// SimulateCanary is the public entry point. Resolves the preset via
// LookupPreset (returns ErrSimulatorUnknownPreset for the closed-set
// gate), takes the recent-traffic samples + error rate + p95 latency
// as inputs (either via WithSamples for tests, or supplied by the
// caller — the CLI fetches them from the Store and projects
// Invocation → InvocationSample at the seam), projects per step, and
// returns a SimReport.
//
// Window is fixed at 1h (issue #976 / ADR-122 explicitly mentions the
// 1h look-back for canary-step projections). Future PR could lift
// this to a parameter; PR-E keeps it pinned.
//
// observedTrafficPerSec is derived from len(recentInvocations) /
// window.Seconds(). When the sample is empty (< 5 invocations), we
// return a SimReport with Note="insufficient traffic sample" and
// every per-step success_p clamped to 0.5 (the maximum-entropy
// neutral default — better than pretending we know the answer).
func SimulateCanary(ctx context.Context, source SimulatorSource, appID, presetName string, invs []InvocationSample, opts ...SimulatorOption) (SimReport, error) {
	cfg := simulatorConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}

	preset, ok := LookupPreset(presetName)
	if !ok {
		return SimReport{}, fmt.Errorf("%w: %q", ErrSimulatorUnknownPreset, presetName)
	}
	if len(preset.Stages) == 0 {
		return SimReport{}, fmt.Errorf("%w: preset=%s", ErrSimulatorEmptyStages, presetName)
	}

	windowEnd := time.Now().UTC()
	windowStart := windowEnd.Add(-1 * time.Hour)

	var (
		errRate float64
		p95Ms   float64
	)
	if cfg.override {
		errRate = cfg.errRate
		p95Ms = cfg.p95Ms
	} else {
		if source == nil {
			return SimReport{}, ErrSimulatorNoSource
		}
		// Pull the two aggregate signals in parallel — they're
		// independent reads. Sequential is fine for the read
		// volume (2 small queries against the same appID) but
		// parallel shaves ~20ms off the cold path; future PR can
		// move this to errgroup if the latency matters.
		type sample struct {
			errRate, p95Ms float64
			err            error
		}
		resCh := make(chan sample, 1)
		go func() {
			er, err := source.RecentErrorRate(ctx, appID, windowStart)
			resCh <- sample{errRate: er, err: err}
		}()
		p95Ch := make(chan sample, 1)
		go func() {
			p, err := source.RecentP95LatencyMs(ctx, appID, windowStart)
			p95Ch <- sample{p95Ms: p, err: err}
		}()
		s := <-resCh
		if s.err != nil {
			return SimReport{}, fmt.Errorf("canary: simulator recent_error_rate: %w", s.err)
		}
		p95s := <-p95Ch
		if p95s.err != nil {
			return SimReport{}, fmt.Errorf("canary: simulator recent_p95_latency_ms: %w", p95s.err)
		}
		errRate = s.errRate
		p95Ms = p95s.p95Ms
	}

	observedTraffic := int64(len(invs))
	if cfg.trafficOverride {
		observedTraffic = cfg.traffic
	}
	// observedTrafficPerSec = invocations / window.Seconds().
	// Window is 1h so this is rph / 3600.
	windowDur := windowEnd.Sub(windowStart)
	observedTrafficPerSec := float64(observedTraffic) / windowDur.Seconds()

	report := SimReport{
		Preset:           presetName,
		Stages:           len(preset.Stages),
		ProjectedSuccess: 1.0,
		PerStep:          make([]SimStepEntry, 0, len(preset.Stages)),
		ObservedTraffic:  clampTrafficCount(observedTraffic),
		ObservedError:    errRate,
		ObservedP95Ms:    p95Ms,
		WindowStart:      windowStart,
		WindowEnd:        windowEnd,
	}

	if observedTraffic < 5 {
		report.Note = "insufficient traffic sample (< 5 invocations in window); per-step projection clamped to 0.5 (maximum-entropy neutral)"
		for i, st := range preset.Stages {
			report.PerStep = append(report.PerStep, SimStepEntry{
				Step:     i,
				Percent:  st.Percent,
				Duration: st.Duration.String(),
				SuccessP: 0.5,
			})
			// Neutral: 0.5^N overall success.
			report.ProjectedSuccess *= 0.5
		}
		return report, nil
	}

	// Happy path: project per-step + cumulative.
	for i, st := range preset.Stages {
		p := simulateStep(observedTrafficPerSec, errRate, st.Percent, st.Duration)
		report.PerStep = append(report.PerStep, SimStepEntry{
			Step:     i,
			Percent:  st.Percent,
			Duration: st.Duration.String(),
			SuccessP: p,
		})
		report.ProjectedSuccess *= p
	}
	return report, nil
}

func clampTrafficCount(count int64) int {
	if count <= 0 {
		return 0
	}
	maxInt := int64(^uint(0) >> 1)
	if count > maxInt {
		return int(maxInt)
	}
	return int(count)
}

// FormatTable renders SimReport as a fixed-width text table for the
// CLI's default output mode. Kept here so the simulator owns the
// presentation (a future PR adding `--format=html` reuses the same
// struct).
func (r SimReport) FormatTable() string {
	const header = "step  percent  duration  success_p"
	rows := make([]string, 0, len(r.PerStep)+2)
	rows = append(rows, header)
	// Sort by step for stable display; PerStep is already in
	// stage-order from the projection loop, but defensive against
	// a future refactor.
	sort.SliceStable(r.PerStep, func(i, j int) bool { return r.PerStep[i].Step < r.PerStep[j].Step })
	for _, s := range r.PerStep {
		rows = append(rows, fmt.Sprintf("%-4d  %-7d  %-8s  %.4f", s.Step, s.Percent, s.Duration, s.SuccessP))
	}
	rows = append(rows, fmt.Sprintf("overall: projected_success_p=%.4f", r.ProjectedSuccess))
	out := ""
	for _, row := range rows {
		out += row + "\n"
	}
	return out
}

// SampleFromInvocation projects a pkg/state.Invocation row down to
// the simulator's minimal InvocationSample. This keeps pkg/api/canary
// free of pkg/state imports — pkg/state imports pkg/api, so the
// inverse direction would create a cycle. The adapter lives here so
// the production cmd/gregale command (which imports both packages)
// has a one-call seam.
//
// Failed is true when the row is in a terminal-failure state
// ('failed' or 'dead_letter'); other terminal states (completed,
// cancelled) are not failures. CreatedAt + CompletedAt are
// forwarded as-is; the p95 helper ignores rows where
// CompletedAt.IsZero(). Nil CompletedAt is projected as a zero
// time.Time; the p95 helper's `dur < 0` guard handles it.
//
// StateStr is the raw InvocationState enum value ("failed",
// "dead_letter", "completed", "cancelled", etc.). The function
// does not import pkg/state — the caller passes the string.
func SampleFromInvocation(createdAt, completedAt time.Time, stateStr string) InvocationSample {
	failed := stateStr == "failed" || stateStr == "dead_letter"
	return InvocationSample{
		CreatedAt:   createdAt,
		CompletedAt: completedAt,
		Failed:      failed,
	}
}
