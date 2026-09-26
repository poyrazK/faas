// preset_test.go — pkg/canary.Once unit tests.
//
// Builds a Progression with a stub Store + APID client so the
// per-row tick logic can be exercised without spinning up Postgres
// or apid. Mirrors the stubStore pattern at
// pkg/safedeploy/orchestrator_test.go so the per-package test
// conventions stay uniform across the SAFE-RELEASES cluster.
//
// The two tests here pin the SAFE-RELEASES code-review hardening
// for finding #1 (canary.Once zero-timestamp defensive guard,
// pkg/canary/preset.go:226) and the runtime invariant the
// migration 00517 NOT NULL DEFAULT NOW() schema change exists to
// uphold.
package canary

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	canarycatalog "github.com/onebox-faas/faas/pkg/api/canary"
	"github.com/onebox-faas/faas/pkg/wire"
)

// stubStore satisfies the canary.Store interface for tests.
type stubStore struct {
	mu            sync.Mutex
	rows          []CanaryRow
	listErr       error
	mirrorSummary MirrorSummary
	mirrorErr     error
	healthSignals []HealthSignal
	healthErr     error
}

type baseOnlyStore struct{ rows []CanaryRow }

func (s *baseOnlyStore) ListCanaryInFlight(context.Context) ([]CanaryRow, error) {
	return append([]CanaryRow(nil), s.rows...), nil
}

func (s *stubStore) ListCanaryInFlight(ctx context.Context) ([]CanaryRow, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]CanaryRow, len(s.rows))
	copy(out, s.rows)
	return out, nil
}

func (s *stubStore) CircuitBreakerObservation(context.Context, CanaryRow, time.Time, time.Time) (CircuitBreakerObservation, error) {
	// Existing progression tests model a first deployment unless they opt into
	// an explicit candidate/stable comparison below.
	return CircuitBreakerObservation{OOMSignalAvailable: true}, nil
}

func (s *stubStore) MirrorSummaryForDeployment(_ context.Context, _, _ string, _ time.Time) (MirrorSummary, error) {
	return s.mirrorSummary, s.mirrorErr
}

func (s *stubStore) FiringSafeReleaseSignals(_ context.Context, _ string) ([]HealthSignal, error) {
	if s.healthErr != nil {
		return nil, s.healthErr
	}
	out := make([]HealthSignal, len(s.healthSignals))
	copy(out, s.healthSignals)
	return out, nil
}

// stubAPID satisfies the APIDClient interface for tests by capturing
type stubAPID struct {
	mu         sync.Mutex
	advances   []string
	expected   []int
	err        error
	recoveries []struct {
		slug, action, reason string
	}
}

func (a *stubAPID) AdvanceCanary(ctx context.Context, id string, expectedStep int) (api.CanaryAdvanceResponse, error) {
	if a.err != nil {
		return api.CanaryAdvanceResponse{}, a.err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.advances = append(a.advances, id)
	a.expected = append(a.expected, expectedStep)
	return api.CanaryAdvanceResponse{}, nil
}

func (a *stubAPID) RecoverRollout(_ context.Context, slug, action, reason string) (api.RolloutTransitionResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.recoveries = append(a.recoveries, struct {
		slug, action, reason string
	}{slug: slug, action: action, reason: reason})
	return api.RolloutTransitionResponse{}, nil
}

type observedCircuitBreakerStore struct {
	*stubStore
	observation CircuitBreakerObservation
	err         error
}

func (s *observedCircuitBreakerStore) CircuitBreakerObservation(context.Context, CanaryRow, time.Time, time.Time) (CircuitBreakerObservation, error) {
	return s.observation, s.err
}

type exactRecoveryStub struct {
	*stubAPID
	deploymentID   string
	predecessorID  string
	action         string
	reason         string
	idempotencyKey string
	err            error
}

func (a *exactRecoveryStub) RecoverDeploymentRolloutAndIdempotencyKey(_ context.Context, deploymentID, predecessorID, action, reason, key string) (api.RolloutTransitionResponse, error) {
	a.deploymentID = deploymentID
	a.predecessorID = predecessorID
	a.action = action
	a.reason = reason
	a.idempotencyKey = key
	return api.RolloutTransitionResponse{}, a.err
}

func mirrorCleanRow(t *testing.T, now time.Time) CanaryRow {
	t.Helper()
	raw, err := json.Marshal([]canarycatalog.CustomStage{
		{Percent: 1, Duration: "1s", MirrorClean: &canarycatalog.MirrorCleanCondition{MinInvocations: 2, WindowSeconds: 300}},
		{Percent: 100, Duration: "0s"},
	})
	if err != nil {
		t.Fatalf("marshal mirror_clean stages: %v", err)
	}
	return CanaryRow{
		ID:                "00000000-0000-0000-0000-000000000001",
		AppID:             "00000000-0000-0000-0000-000000000002",
		AppSlug:           "demo",
		CanaryPreset:      "custom",
		CanaryStep:        0,
		CanaryTotalSteps:  2,
		CanaryStepStarted: now.Add(-time.Minute),
		RolloutState:      "rolling_out",
		CanaryStages:      raw,
	}
}

func TestProgressionOnce_MirrorCleanGate(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name        string
		summary     MirrorSummary
		wantAdvance int
		wantAbort   int
		wantWait    int
		wantReason  string
	}{
		{name: "clean and enough traffic", summary: MirrorSummary{TotalInvocations: 2}, wantAdvance: 1},
		{name: "clean but insufficient traffic", summary: MirrorSummary{TotalInvocations: 1}, wantWait: 1},
		{name: "drift aborts", summary: MirrorSummary{TotalInvocations: 1, StatusDiffCount: 1, BodyDiffCount: 2}, wantAbort: 1, wantReason: "status_diff_count=1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &stubStore{rows: []CanaryRow{mirrorCleanRow(t, now)}, mirrorSummary: tc.summary}
			apid := &stubAPID{}
			prog := NewProgression(store, apid, nil, slog.Default())
			prog.Now = func() time.Time { return now }

			stats, err := prog.Once(context.Background())
			if err != nil {
				t.Fatalf("Once: %v", err)
			}
			if stats.Advanced != tc.wantAdvance || stats.Aborted != tc.wantAbort || stats.SkippedMirrorNotReady != tc.wantWait {
				t.Fatalf("stats = %+v; want advance=%d abort=%d wait=%d", stats, tc.wantAdvance, tc.wantAbort, tc.wantWait)
			}
			if len(apid.advances) != tc.wantAdvance {
				t.Errorf("advances = %d; want %d", len(apid.advances), tc.wantAdvance)
			}
			if len(apid.recoveries) != tc.wantAbort {
				t.Errorf("recoveries = %d; want %d", len(apid.recoveries), tc.wantAbort)
			}
			if tc.wantReason != "" && (len(apid.recoveries) != 1 || !strings.Contains(apid.recoveries[0].reason, tc.wantReason)) {
				t.Errorf("abort reason = %q; want substring %q", apid.recoveries[0].reason, tc.wantReason)
			}
		})
	}
}

func TestProgressionOnce_HealthGateHoldsPromotion(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	store := &stubStore{
		rows: []CanaryRow{{
			ID:                "00000000-0000-0000-0000-000000000001",
			AppID:             "00000000-0000-0000-0000-000000000002",
			CanaryPreset:      "balanced",
			CanaryStep:        0,
			CanaryTotalSteps:  4,
			CanaryStepStarted: now.Add(-time.Hour),
			RolloutState:      "rolling_out",
		}},
		healthSignals: []HealthSignal{{
			RuleID:   "rule-1",
			RuleName: "checkout errors",
			Metric:   "error_rate_pct",
			Action:   "rollback",
		}},
	}
	apid := &stubAPID{}
	prog := NewProgression(store, apid, nil, slog.Default())
	prog.Now = func() time.Time { return now }

	stats, err := prog.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if stats.SkippedHealthGate != 1 {
		t.Fatalf("SkippedHealthGate = %d, want 1", stats.SkippedHealthGate)
	}
	if stats.Advanced != 0 || len(apid.advances) != 0 {
		t.Fatalf("health-gated canary advanced: stats=%+v advances=%d", stats, len(apid.advances))
	}
}

func TestProgressionOnce_HealthGateReadErrorFailsClosed(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	store := &stubStore{
		rows: []CanaryRow{{
			ID:                "00000000-0000-0000-0000-000000000001",
			AppID:             "00000000-0000-0000-0000-000000000002",
			CanaryPreset:      "balanced",
			CanaryStep:        0,
			CanaryTotalSteps:  4,
			CanaryStepStarted: now.Add(-time.Hour),
			RolloutState:      "rolling_out",
		}},
		healthErr: errors.New("alert store unavailable"),
	}
	apid := &stubAPID{}
	prog := NewProgression(store, apid, nil, slog.Default())
	prog.Now = func() time.Time { return now }

	stats, err := prog.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if stats.Errors != 1 || stats.Advanced != 0 || len(apid.advances) != 0 {
		t.Fatalf("health-gate read error was not fail-closed: stats=%+v advances=%d", stats, len(apid.advances))
	}
}

func TestProgressionOnce_CircuitBreakerAbortsExactCandidate(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	const candidateID = "00000000-0000-0000-0000-000000000001"
	const appID = "00000000-0000-0000-0000-000000000002"
	const stableID = "00000000-0000-0000-0000-000000000003"
	store := &observedCircuitBreakerStore{
		stubStore: &stubStore{rows: []CanaryRow{{
			ID:                candidateID,
			AppID:             appID,
			CanaryPreset:      "balanced",
			CanaryStep:        0,
			CanaryTotalSteps:  4,
			CanaryStepStarted: now.Add(-time.Hour),
			RolloutState:      "rolling_out",
		}}},
		observation: CircuitBreakerObservation{
			Candidate:                 HealthWindow{Requests: 100, ServerErrors: 20, P95LatencyMS: 100},
			Stable:                    HealthWindow{Requests: 100, ServerErrors: 0, P95LatencyMS: 100},
			StableDeploymentID:        stableID,
			HasStable:                 true,
			OOMSignalAvailable:        true,
			DependencySignalAvailable: true,
		},
	}
	apid := &exactRecoveryStub{stubAPID: &stubAPID{}}
	ops := wire.NewOpsMetrics("meterd")
	progression := NewProgression(store, apid, ops, slog.Default())
	progression.Now = func() time.Time { return now }

	stats, err := progression.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if stats.CircuitBreakerAborted != 1 || stats.Aborted != 1 || stats.Advanced != 0 {
		t.Fatalf("stats = %+v; want one circuit-breaker abort and no advance", stats)
	}
	if apid.deploymentID != candidateID || apid.predecessorID != stableID || apid.action != "abort" {
		t.Fatalf("recovery target = candidate:%q predecessor:%q action:%q; want exact candidate/predecessor abort", apid.deploymentID, apid.predecessorID, apid.action)
	}
	if !strings.Contains(apid.reason, "5xx") || apid.idempotencyKey != "canary-circuit-breaker-abort-"+candidateID+"-"+stableID {
		t.Fatalf("recovery reason/key = %q / %q; want stable 5xx reason and deterministic idempotency key", apid.reason, apid.idempotencyKey)
	}
	if got := testutil.ToFloat64(ops.CanaryProgressionCircuitBreakerTotal("abort_5xx")); got != 1 {
		t.Fatalf("abort_5xx metric = %g, want 1", got)
	}
}

func TestProgressionOnce_CircuitBreakerHoldsLowTrafficAndExportsEvent(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	store := &observedCircuitBreakerStore{
		stubStore: &stubStore{rows: []CanaryRow{{
			ID:                "00000000-0000-0000-0000-000000000001",
			AppID:             "00000000-0000-0000-0000-000000000002",
			CanaryPreset:      "balanced",
			CanaryStep:        0,
			CanaryTotalSteps:  4,
			CanaryStepStarted: now.Add(-time.Hour),
			RolloutState:      "rolling_out",
		}}},
		observation: CircuitBreakerObservation{
			Candidate:                 HealthWindow{Requests: CircuitBreakerMinRequests - 1},
			Stable:                    HealthWindow{Requests: 100},
			StableDeploymentID:        "00000000-0000-0000-0000-000000000003",
			HasStable:                 true,
			OOMSignalAvailable:        true,
			DependencySignalAvailable: true,
		},
	}
	ops := wire.NewOpsMetrics("meterd")
	progression := NewProgression(store, &stubAPID{}, ops, slog.Default())
	progression.Now = func() time.Time { return now }

	stats, err := progression.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if stats.SkippedCircuitBreaker != 1 || stats.Advanced != 0 {
		t.Fatalf("stats = %+v; want one hold and no advance", stats)
	}
	if got := testutil.ToFloat64(ops.CanaryProgressionCircuitBreakerTotal("hold_insufficient_samples")); got != 1 {
		t.Fatalf("hold_insufficient_samples metric = %g, want 1", got)
	}
}

func TestProgressionOnce_CircuitBreakerMissingStoreAdapterExportsEvent(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	store := &baseOnlyStore{rows: []CanaryRow{{
		ID:                "00000000-0000-0000-0000-000000000001",
		AppID:             "00000000-0000-0000-0000-000000000002",
		CanaryPreset:      "balanced",
		CanaryStep:        0,
		CanaryTotalSteps:  4,
		CanaryStepStarted: now.Add(-time.Hour),
		RolloutState:      "rolling_out",
	}}}
	ops := wire.NewOpsMetrics("meterd")
	progression := NewProgression(store, &stubAPID{}, ops, slog.Default())
	progression.Now = func() time.Time { return now }

	stats, err := progression.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if stats.Errors != 1 || stats.SkippedCircuitBreaker != 1 || stats.Advanced != 0 {
		t.Fatalf("stats = %+v; want missing adapter to fail closed", stats)
	}
	if got := testutil.ToFloat64(ops.CanaryProgressionCircuitBreakerTotal("hold_observation_unavailable")); got != 1 {
		t.Fatalf("hold_observation_unavailable metric = %g, want 1", got)
	}
}

func TestProgressionOnce_CircuitBreakerColdBootAbortHasSpecificReason(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	const candidateID = "00000000-0000-0000-0000-000000000001"
	const appID = "00000000-0000-0000-0000-000000000002"
	const stableID = "00000000-0000-0000-0000-000000000003"
	store := &observedCircuitBreakerStore{
		stubStore: &stubStore{rows: []CanaryRow{{
			ID:                candidateID,
			AppID:             appID,
			CanaryPreset:      "balanced",
			CanaryStep:        0,
			CanaryTotalSteps:  4,
			CanaryStepStarted: now.Add(-time.Hour),
			RolloutState:      "rolling_out",
		}}},
		observation: CircuitBreakerObservation{
			Candidate: HealthWindow{
				Requests: 100, P95LatencyMS: 100,
				ColdBootRequests: CircuitBreakerMinColdBootRequests, ColdBootP95LatencyMS: 2000,
			},
			Stable: HealthWindow{
				Requests: 100, P95LatencyMS: 100,
				ColdBootRequests: CircuitBreakerMinColdBootRequests, ColdBootP95LatencyMS: 800,
			},
			StableDeploymentID:        stableID,
			HasStable:                 true,
			OOMSignalAvailable:        true,
			DependencySignalAvailable: true,
		},
	}
	apid := &exactRecoveryStub{stubAPID: &stubAPID{}}
	ops := wire.NewOpsMetrics("meterd")
	progression := NewProgression(store, apid, ops, slog.Default())
	progression.Now = func() time.Time { return now }

	stats, err := progression.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if stats.CircuitBreakerAborted != 1 || !strings.Contains(apid.reason, "cold-boot request latency") {
		t.Fatalf("stats/reason = %+v / %q; want exact cold-boot regression abort", stats, apid.reason)
	}
	if got := testutil.ToFloat64(ops.CanaryProgressionCircuitBreakerTotal("abort_cold_boot_p95")); got != 1 {
		t.Fatalf("abort_cold_boot_p95 metric = %g, want 1", got)
	}
}

func TestProgressionOnce_CircuitBreakerCPUPerRequestAbortHasSpecificReason(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	const candidateID = "00000000-0000-0000-0000-000000000001"
	const appID = "00000000-0000-0000-0000-000000000002"
	const stableID = "00000000-0000-0000-0000-000000000003"
	store := &observedCircuitBreakerStore{
		stubStore: &stubStore{rows: []CanaryRow{{
			ID:                candidateID,
			AppID:             appID,
			CanaryPreset:      "balanced",
			CanaryStep:        0,
			CanaryTotalSteps:  4,
			CanaryStepStarted: now.Add(-time.Hour),
			RolloutState:      "rolling_out",
		}}},
		observation: CircuitBreakerObservation{
			Candidate:                 HealthWindow{Requests: 100, P95LatencyMS: 100, CPURequests: 100, CPUUsec: 80_000_000},
			Stable:                    HealthWindow{Requests: 100, P95LatencyMS: 100, CPURequests: 100, CPUUsec: 10_000_000},
			StableDeploymentID:        stableID,
			HasStable:                 true,
			OOMSignalAvailable:        true,
			CPURequestSignalAvailable: true,
			DependencySignalAvailable: true,
		},
	}
	apid := &exactRecoveryStub{stubAPID: &stubAPID{}}
	ops := wire.NewOpsMetrics("meterd")
	progression := NewProgression(store, apid, ops, slog.Default())
	progression.Now = func() time.Time { return now }

	stats, err := progression.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if stats.CircuitBreakerAborted != 1 || !strings.Contains(apid.reason, "CPU per request") {
		t.Fatalf("stats/reason = %+v / %q; want CPU/request regression abort", stats, apid.reason)
	}
	if got := testutil.ToFloat64(ops.CanaryProgressionCircuitBreakerTotal("abort_cpu_per_request")); got != 1 {
		t.Fatalf("abort_cpu_per_request metric = %g, want 1", got)
	}
}

func TestProgressionOnce_CircuitBreakerDependencyErrorAbortHasSpecificReason(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	const candidateID = "00000000-0000-0000-0000-000000000001"
	const appID = "00000000-0000-0000-0000-000000000002"
	const stableID = "00000000-0000-0000-0000-000000000003"
	store := &observedCircuitBreakerStore{
		stubStore: &stubStore{rows: []CanaryRow{{
			ID:                candidateID,
			AppID:             appID,
			CanaryPreset:      "balanced",
			CanaryStep:        0,
			CanaryTotalSteps:  4,
			CanaryStepStarted: now.Add(-time.Hour),
			RolloutState:      "rolling_out",
		}}},
		observation: CircuitBreakerObservation{
			Candidate:                 HealthWindow{Requests: 100, P95LatencyMS: 100, CPURequests: 100, CPUUsec: 10_000_000, DependencyCalls: 20, DependencyErrors: 7},
			Stable:                    HealthWindow{Requests: 100, P95LatencyMS: 100, CPURequests: 100, CPUUsec: 10_000_000, DependencyCalls: 40},
			StableDeploymentID:        stableID,
			HasStable:                 true,
			OOMSignalAvailable:        true,
			CPURequestSignalAvailable: true,
			DependencySignalAvailable: true,
		},
	}
	apid := &exactRecoveryStub{stubAPID: &stubAPID{}}
	ops := wire.NewOpsMetrics("meterd")
	progression := NewProgression(store, apid, ops, slog.Default())
	progression.Now = func() time.Time { return now }

	stats, err := progression.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if stats.CircuitBreakerAborted != 1 || !strings.Contains(apid.reason, "managed dependency error") {
		t.Fatalf("stats/reason = %+v / %q; want managed dependency error abort", stats, apid.reason)
	}
	if apid.deploymentID != candidateID || apid.predecessorID != stableID {
		t.Fatalf("recovery target = candidate:%q predecessor:%q; want exact candidate and stable predecessor", apid.deploymentID, apid.predecessorID)
	}
	if got := testutil.ToFloat64(ops.CanaryProgressionCircuitBreakerTotal("abort_dependency_errors")); got != 1 {
		t.Fatalf("abort_dependency_errors metric = %g, want 1", got)
	}
}

// TestProgressionOnce_ZeroTimestampDefensiveGuard — code-review
// finding #1 hardening. When a row's CanaryStepStarted is the zero
// time (which post-migration 00517 should never happen because the
// column is NOT NULL DEFAULT NOW(), but a defensive belt-and-braces
// case for a future write path that bypasses the schema default),
// the tick MUST (a) still run the wall-clock check (so behaviour is
// unchanged from pre-migration: elapsed = 56y > Duration → advance)
// and (b) bump CanaryProgressionZeroTimestampTotal for operator
// visibility.
//
// We don't assert on the Ops counter directly here (that requires
// constructing a full wire.OpsMetrics with a Prometheus registry
// — heavyweight for a unit test). The logging path is asserted
// via the Stats.Advanced counter instead: a zero-time row must
// advance on the first tick, identical to the pre-migration
// behavior, AND the Ops counter increment happens via the
// nil-safe accessor.
func TestProgressionOnce_ZeroTimestampDefensiveGuard(t *testing.T) {
	store := &stubStore{
		rows: []CanaryRow{
			{
				ID:                "00000000-0000-0000-0000-000000000001",
				AppID:             "00000000-0000-0000-0000-000000000002",
				CanaryPreset:      "balanced",
				CanaryStep:        0,           // advance from step 0 → step 1
				CanaryTotalSteps:  4,           // balanced has 4 stages
				CanaryStepStarted: time.Time{}, // zero time — defensive path
				RolloutState:      "rolling_out",
				CanaryStages:      nil,
			},
		},
	}
	apid := &stubAPID{}
	// Nil Ops is the nil-safe path; the runtime does not panic on
	// nil receiver, the counter increment silently no-ops. The
	// production wiring in cmd/meterd passes a real *wire.OpsMetrics.
	prog := NewProgression(store, apid, nil, slog.Default())

	stats, err := prog.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if stats.Advanced != 1 {
		t.Errorf("stats.Advanced = %d, want 1 (zero-time defensive path must still advance — elapsed = 56y > Duration)", stats.Advanced)
	}
	if len(apid.advances) != 1 {
		t.Errorf("apid advances = %d, want 1 (the defensive guard must still run the wall-clock check and advance)", len(apid.advances))
	}
	if got := apid.expected[0]; got != 0 {
		t.Errorf("expected step = %d, want 0 (APID derives the next traffic percentage)", got)
	}
}

// TestProgressionOnce_WallClockSkipsWhenNotElapsed — the pre-PR
// happy path: when CanaryStepStarted is a real timestamp within the
// current stage's Duration, the tick must skip with
// SkippedNotElapsed. Pins the behaviour the code-review hardening
// must NOT regress.
func TestProgressionOnce_WallClockSkipsWhenNotElapsed(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	store := &stubStore{
		rows: []CanaryRow{
			{
				ID:                "00000000-0000-0000-0000-000000000001",
				AppID:             "00000000-0000-0000-0000-000000000002",
				CanaryPreset:      "balanced",
				CanaryStep:        0,
				CanaryTotalSteps:  4,
				CanaryStepStarted: now.Add(-30 * time.Second), // 30s ago — balanced step 0 Duration is 30s
				RolloutState:      "rolling_out",
				CanaryStages:      nil,
			},
		},
	}
	apid := &stubAPID{}
	prog := NewProgression(store, apid, nil, slog.Default())
	prog.Now = func() time.Time { return now }

	stats, err := prog.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if stats.SkippedNotElapsed != 1 {
		t.Errorf("stats.SkippedNotElapsed = %d, want 1 (30s elapsed < 30s Duration → skip)", stats.SkippedNotElapsed)
	}
	if stats.Advanced != 0 {
		t.Errorf("stats.Advanced = %d, want 0 (not yet elapsed)", stats.Advanced)
	}
	if len(apid.advances) != 0 {
		t.Errorf("apid advances = %d, want 0 (must not advance when not yet elapsed)", len(apid.advances))
	}
}

// TestProgressionOnce_AdvancedTotalLabeledPerPreset — SAFE-RELEASES-OBS
// PR-A: pin the canary_preset label pass-through. Pre-PR the
// canary_progression_advanced_total counter was unlabelled (operators
// could only see fleet-wide rollup). PR-A re-labels it with the
// row's CanaryPreset value (closed-set: none/slow/balanced/
// aggressive/1-10-50-100/custom; unknown presets drop to the no-op
// closure so cardinality stays bounded). This test asserts:
//
//	(a) a valid preset name lands on the labeled series;
//	(b) an unknown preset name (e.g. a typo'd column write) drops to
//	    the no-op path so the closed-vocab gate is honored.
//
// Reads the counter via testutil.ToFloat64 on the per-label
// counter returned from OpsMetrics.CanaryProgressionAdvancedTotal(preset).
func TestProgressionOnce_AdvancedTotalLabeledPerPreset(t *testing.T) {
	now := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	store := &stubStore{
		rows: []CanaryRow{
			{
				ID:                "00000000-0000-0000-0000-000000000001",
				AppID:             "00000000-0000-0000-0000-000000000002",
				CanaryPreset:      "balanced",
				CanaryStep:        0,
				CanaryTotalSteps:  3,
				CanaryStepStarted: now.Add(-1 * time.Hour), // elapsed > balanced step-0 duration
				RolloutState:      "rolling_out",
			},
		},
	}
	apid := &stubAPID{}
	ops := wire.NewOpsMetrics("canary_test_obs_pr_a_balanced")
	prog := NewProgression(store, apid, ops, slog.Default())
	prog.Now = func() time.Time { return now }

	stats, err := prog.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if stats.Advanced != 1 {
		t.Fatalf("stats.Advanced = %d, want 1", stats.Advanced)
	}

	// Balanced preset → labeled series bumps by 1.
	c := ops.CanaryProgressionAdvancedTotal("balanced")
	if c == nil {
		t.Fatal("CanaryProgressionAdvancedTotal returned nil for valid preset 'balanced'")
	}
	if got := testutil.ToFloat64(c); got != 1 {
		t.Errorf("canary_progression_advanced_total{canary_preset=balanced} = %v, want 1", got)
	}
}

// TestProgressionOnce_AdvancedTotalUnknownPresetDrops pins the
// closed-vocabulary gate at the accessor level. When a row has a
// typo'd preset name (the schema CHECK doesn't gate canary_preset —
// migration 00480 leaves the column as TEXT), the accessor must
// return nil so the Inc() call is a no-op AND Prometheus cardinality
// stays bounded. No panic, no silent series inflation.
func TestProgressionOnce_AdvancedTotalUnknownPresetDrops(t *testing.T) {
	ops := wire.NewOpsMetrics("canary_test_obs_pr_a_unknown")
	c := ops.CanaryProgressionAdvancedTotal("unknown_preset_name")
	if c != nil {
		t.Errorf("expected nil counter for unknown preset; got %v", testutil.ToFloat64(c))
	}
}
