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
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

// stubStore satisfies the canary.Store interface for tests.
type stubStore struct {
	mu        sync.Mutex
	rows      []CanaryRow
	auditRows []AuditEntry
	listErr   error
	auditErr  error
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

func (s *stubStore) AppendDeploymentAudit(ctx context.Context, entry AuditEntry) (int64, error) {
	if s.auditErr != nil {
		return 0, s.auditErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditRows = append(s.auditRows, entry)
	return int64(len(s.auditRows)), nil
}

// stubAPID satisfies the APIDClient interface for tests by capturing
// the (deployment_id, percent) tuples the Progression passed to it.
// Returns a zero api.DeploymentResponse — pkg/canary.Once doesn't
// inspect the return value.
type stubAPID struct {
	mu      sync.Mutex
	patches []string
	percent []int
	err     error
}

func (a *stubAPID) PatchDeploymentsIdTraffic(ctx context.Context, id string, percent int) (api.DeploymentResponse, error) {
	if a.err != nil {
		return api.DeploymentResponse{}, a.err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.patches = append(a.patches, id)
	a.percent = append(a.percent, percent)
	return api.DeploymentResponse{}, nil
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
				AccountID:         "00000000-0000-0000-0000-000000000003",
				CanaryPreset:      "balanced",
				CanaryStep:        0,           // advance from step 0 → step 1
				CanaryTotalSteps:  3,           // balanced has 3 stages
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
	prog := NewProgression(store, apid, nil, slog.Default(), "test:actor", "acct-uuid")

	stats, err := prog.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if stats.Advanced != 1 {
		t.Errorf("stats.Advanced = %d, want 1 (zero-time defensive path must still advance — elapsed = 56y > Duration)", stats.Advanced)
	}
	if len(apid.patches) != 1 {
		t.Errorf("apid patches = %d, want 1 (the defensive guard must still run the wall-clock check and advance)", len(apid.patches))
	}
	if got := apid.percent[0]; got != 10 {
		t.Errorf("patched percent = %d, want 10 (balanced step 1 → 10%%)", got)
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
				AccountID:         "00000000-0000-0000-0000-000000000003",
				CanaryPreset:      "balanced",
				CanaryStep:        0,
				CanaryTotalSteps:  3,
				CanaryStepStarted: now.Add(-30 * time.Second), // 30s ago — balanced step 0 Duration is 30s
				RolloutState:      "rolling_out",
				CanaryStages:      nil,
			},
		},
	}
	apid := &stubAPID{}
	prog := NewProgression(store, apid, nil, slog.Default(), "test:actor", "acct-uuid")
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
	if len(apid.patches) != 0 {
		t.Errorf("apid patches = %d, want 0 (must not patch when not yet elapsed)", len(apid.patches))
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
				AccountID:         "00000000-0000-0000-0000-000000000003",
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
	prog := NewProgression(store, apid, ops, slog.Default(), "test:actor", "acct-uuid")
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
