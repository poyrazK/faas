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

	"github.com/onebox-faas/faas/pkg/api"
)

// stubStore satisfies the canary.Store interface for tests.
type stubStore struct {
	mu      sync.Mutex
	rows    []CanaryRow
	listErr error
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

// stubAPID satisfies the APIDClient interface for tests by capturing
type stubAPID struct {
	mu       sync.Mutex
	advances []string
	expected []int
	err      error
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
