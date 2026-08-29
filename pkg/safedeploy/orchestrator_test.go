// orchestrator_test.go — pkg/safedeploy.Orchestrator unit tests.
// Mirrors the patterns at pkg/canary/preset_test.go + cmd/meterd/
// alert_presets_ticks_test.go so the per-package test conventions
// stay uniform across the SAFE-RELEASES cluster.
package safedeploy

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// stubStore is the in-memory test seam for the orchestrator's
// Store interface. Tracks every method call so tests can assert
// the per-row decision tree without spinning up Postgres.
type stubStore struct {
	mu         sync.Mutex
	rollouts   map[string]state.Deployment
	auditRows  []state.DeploymentAudit
	listErr    error // optional; forces ListPendingRollouts to fail
	stampErr   error // optional; forces SafedeployStampRollout to fail
	auditErr   error // optional; forces AppendDeploymentAudit to fail
	listCalls  int
	stampCalls int
	auditCalls int
}

func newStubStore() *stubStore {
	return &stubStore{rollouts: map[string]state.Deployment{}}
}

func (s *stubStore) SafedeployListPendingRollouts(_ context.Context) ([]state.Deployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listCalls++
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := make([]state.Deployment, 0, len(s.rollouts))
	for _, d := range s.rollouts {
		if d.Status != state.DeployLive {
			continue
		}
		if d.RolloutState != "pending" && d.RolloutState != "rolling_out" {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

func (s *stubStore) SafedeployStampRollout(_ context.Context, id, rolloutState string, startedAt, completedAt, abortedAt *time.Time, abortedReason string) (state.Deployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stampCalls++
	if s.stampErr != nil {
		return state.Deployment{}, s.stampErr
	}
	d, ok := s.rollouts[id]
	if !ok {
		return state.Deployment{}, state.ErrNotFound
	}
	d.RolloutState = rolloutState
	if startedAt != nil {
		t := *startedAt
		d.RolloutStartedAt = &t
	}
	if completedAt != nil {
		t := *completedAt
		d.RolloutCompletedAt = &t
	}
	if abortedAt != nil {
		t := *abortedAt
		d.RolloutAbortedAt = &t
	}
	d.RolloutAbortedReason = abortedReason
	s.rollouts[id] = d
	return d, nil
}

func (s *stubStore) AppendDeploymentAudit(_ context.Context, entry state.DeploymentAudit) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditCalls++
	if s.auditErr != nil {
		return 0, s.auditErr
	}
	s.auditRows = append(s.auditRows, entry)
	return int64(len(s.auditRows)), nil
}

// seedDeployment inserts a deployment row into the stub and
// returns the seeded row (with a fresh UUID so the audit emit
// succeeds — the orchestrator's parseDeploymentUUID guard
// rejects empty IDs).
func seedDeployment(s *stubStore, t *testing.T, mutate func(d *state.Deployment)) state.Deployment {
	t.Helper()
	depID := uuid.New().String()
	d := state.Deployment{
		ID:               depID,
		AppID:            "app-" + depID[:8],
		Status:           state.DeployLive,
		RolloutState:     "pending",
		CanaryPreset:     "balanced",
		CanaryStep:       0,
		CanaryTotalSteps: 4,
		CreatedAt:        time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC),
	}
	if mutate != nil {
		mutate(&d)
	}
	s.mu.Lock()
	s.rollouts[depID] = d
	s.mu.Unlock()
	return d
}

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(discardWriter{}, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// TestOrchestrator_PendingWithLadder_FlipsToRollingOut — a row in
// pending with canary_total_steps>0 must flip to rolling_out
// and stamp rollout_started_at. One audit row emitted.
func TestOrchestrator_PendingWithLadder_FlipsToRollingOut(t *testing.T) {
	store := newStubStore()
	dep := seedDeployment(store, t, nil)
	o := NewOrchestrator(store, discardLog(), "meterd:safedeploy", "")

	stats, _, err := o.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if stats.Started != 1 {
		t.Errorf("stats.Started = %d; want 1", stats.Started)
	}
	if stats.Completed != 0 {
		t.Errorf("stats.Completed = %d; want 0", stats.Completed)
	}
	got := store.rollouts[dep.ID]
	if got.RolloutState != "rolling_out" {
		t.Errorf("rollout_state = %q; want rolling_out", got.RolloutState)
	}
	if got.RolloutStartedAt == nil {
		t.Errorf("rollout_started_at is nil; want non-nil after start")
	}
	if len(store.auditRows) != 1 {
		t.Fatalf("audit rows = %d; want 1", len(store.auditRows))
	}
	if store.auditRows[0].Kind != state.DeploymentAuditKind(AuditKindRolloutStarted) {
		t.Errorf("audit kind = %q; want %q", store.auditRows[0].Kind, AuditKindRolloutStarted)
	}
}

// TestOrchestrator_PendingNoLadder_FlipsToComplete — a row in
// pending with canary_total_steps=0 must short-circuit to
// complete on the first tick. One audit row emitted.
func TestOrchestrator_PendingNoLadder_FlipsToComplete(t *testing.T) {
	store := newStubStore()
	dep := seedDeployment(store, t, func(d *state.Deployment) {
		d.CanaryTotalSteps = 0
	})
	o := NewOrchestrator(store, discardLog(), "meterd:safedeploy", "")

	stats, _, err := o.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if stats.Completed != 1 {
		t.Errorf("stats.Completed = %d; want 1", stats.Completed)
	}
	if stats.Started != 0 {
		t.Errorf("stats.Started = %d; want 0 (no-ladder short-circuits)", stats.Started)
	}
	got := store.rollouts[dep.ID]
	if got.RolloutState != "complete" {
		t.Errorf("rollout_state = %q; want complete", got.RolloutState)
	}
	if got.RolloutCompletedAt == nil {
		t.Errorf("rollout_completed_at is nil; want non-nil after complete")
	}
	if got.RolloutStartedAt != nil {
		t.Errorf("rollout_started_at = %v; want nil (no-ladder bypasses rolling_out)", got.RolloutStartedAt)
	}
	if len(store.auditRows) != 1 {
		t.Fatalf("audit rows = %d; want 1", len(store.auditRows))
	}
	if store.auditRows[0].Kind != state.DeploymentAuditKind(AuditKindRolloutCompleted) {
		t.Errorf("audit kind = %q; want %q", store.auditRows[0].Kind, AuditKindRolloutCompleted)
	}
}

// TestOrchestrator_RollingOutAtTerminal_FlipsToComplete — a row in
// rolling_out with canary_step >= canary_total_steps must flip
// to complete and stamp rollout_completed_at. rollout_started_at
// is preserved (the orchestrator passes through the prior value).
func TestOrchestrator_RollingOutAtTerminal_FlipsToComplete(t *testing.T) {
	store := newStubStore()
	startedAt := time.Date(2026, 7, 28, 11, 30, 0, 0, time.UTC)
	dep := seedDeployment(store, t, func(d *state.Deployment) {
		d.RolloutState = "rolling_out"
		d.CanaryStep = 4
		d.CanaryTotalSteps = 4
		d.RolloutStartedAt = &startedAt
	})
	o := NewOrchestrator(store, discardLog(), "meterd:safedeploy", "")

	stats, _, err := o.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if stats.Completed != 1 {
		t.Errorf("stats.Completed = %d; want 1", stats.Completed)
	}
	got := store.rollouts[dep.ID]
	if got.RolloutState != "complete" {
		t.Errorf("rollout_state = %q; want complete", got.RolloutState)
	}
	if got.RolloutStartedAt == nil || !got.RolloutStartedAt.Equal(startedAt) {
		t.Errorf("rollout_started_at = %v; want preserved %v", got.RolloutStartedAt, startedAt)
	}
	if got.RolloutCompletedAt == nil {
		t.Errorf("rollout_completed_at is nil; want non-nil")
	}
}

// TestOrchestrator_StuckRollout_LogsWarnNoRecover — a rolling_out
// row stuck for > StuckAfterDuration must NOT auto-recover; the
// orchestrator only logs + bumps Stats.StuckDetected. The
// manual CLI (Commit 6) is the escape hatch.
func TestOrchestrator_StuckRollout_LogsWarnNoRecover(t *testing.T) {
	store := newStubStore()
	stuckAt := time.Now().Add(-2 * StuckAfterDuration) // way past stuck
	dep := seedDeployment(store, t, func(d *state.Deployment) {
		d.RolloutState = "rolling_out"
		d.CanaryStep = 1
		d.CanaryTotalSteps = 4
		d.CanaryStepStartedAt = &stuckAt
		d.RolloutStartedAt = &stuckAt
	})
	o := NewOrchestrator(store, discardLog(), "meterd:safedeploy", "")

	stats, _, err := o.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if stats.StuckDetected != 1 {
		t.Errorf("stats.StuckDetected = %d; want 1", stats.StuckDetected)
	}
	if stats.Completed != 0 || stats.Started != 0 {
		t.Errorf("stats = %+v; want no transitions on stuck row", stats)
	}
	got := store.rollouts[dep.ID]
	if got.RolloutState != "rolling_out" {
		t.Errorf("rollout_state = %q; want rolling_out (no auto-recovery)", got.RolloutState)
	}
	if len(store.auditRows) != 0 {
		t.Errorf("audit rows = %d; want 0 (stuck detection emits no audit)", len(store.auditRows))
	}
}

// TestOrchestrator_HealthyInFlight_NoOp — a rolling_out row whose
// canary_step is mid-ladder AND canary_step_started_at is fresh
// (< StuckAfterDuration) must NOT transition; the orchestrator
// is silent for healthy in-flight rows.
func TestOrchestrator_HealthyInFlight_NoOp(t *testing.T) {
	store := newStubStore()
	freshAt := time.Now().Add(-1 * time.Minute) // well under StuckAfterDuration
	dep := seedDeployment(store, t, func(d *state.Deployment) {
		d.RolloutState = "rolling_out"
		d.CanaryStep = 1
		d.CanaryTotalSteps = 4
		d.CanaryStepStartedAt = &freshAt
		d.RolloutStartedAt = &freshAt
	})
	o := NewOrchestrator(store, discardLog(), "meterd:safedeploy", "")

	stats, _, err := o.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if stats.Started+stats.Completed+stats.StuckDetected != 0 {
		t.Errorf("stats = %+v; want zero counters on healthy in-flight row", stats)
	}
	got := store.rollouts[dep.ID]
	if got.RolloutState != "rolling_out" {
		t.Errorf("rollout_state = %q; want rolling_out (no change)", got.RolloutState)
	}
	if len(store.auditRows) != 0 {
		t.Errorf("audit rows = %d; want 0 (healthy row emits no audit)", len(store.auditRows))
	}
}

// TestOrchestrator_ListError_Propagates — a ListPendingRollouts
// failure must surface to the caller; per-tick query failures
// are NOT swallowed (the daemon's runTicks will warn-log +
// continue, but the orchestrator must surface the error so the
// operator can investigate Postgres).
func TestOrchestrator_ListError_Propagates(t *testing.T) {
	store := newStubStore()
	store.listErr = errors.New("synthetic pg blip")
	o := NewOrchestrator(store, discardLog(), "meterd:safedeploy", "")

	_, _, err := o.Once(context.Background())
	if err == nil {
		t.Fatalf("Once: nil err; want non-nil on list failure")
	}
	if !errors.Is(err, store.listErr) {
		t.Errorf("err = %v; want wraps %v", err, store.listErr)
	}
}

// TestOrchestrator_AuditEmitFailed_BumpsCounter — a Postgres
// audit failure must be warn-logged + counted in
// Stats.AuditEmitFailed; the state-machine transition still
// lands (the operator can recover the missing audit row's
// content from the rollout_state column).
func TestOrchestrator_AuditEmitFailed_BumpsCounter(t *testing.T) {
	store := newStubStore()
	seedDeployment(store, t, nil)
	store.auditErr = errors.New("synthetic audit write fail")
	o := NewOrchestrator(store, discardLog(), "meterd:safedeploy", "")

	stats, _, err := o.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if stats.Started != 1 {
		t.Errorf("stats.Started = %d; want 1 (transition still lands)", stats.Started)
	}
	if stats.AuditEmitFailed != 1 {
		t.Errorf("stats.AuditEmitFailed = %d; want 1", stats.AuditEmitFailed)
	}
	// The state-machine write landed even though the audit
	// emit failed — that's the recoverable contract.
	if len(store.rollouts) != 1 {
		t.Fatalf("rollouts len = %d; want 1", len(store.rollouts))
	}
	for _, d := range store.rollouts {
		if d.RolloutState != "rolling_out" {
			t.Errorf("rollout_state = %q; want rolling_out (transition survives audit failure)", d.RolloutState)
		}
	}
}

// TestOrchestrator_NilStore_ReturnsError — calling Once on an
// Orchestrator with a nil Store must surface ErrOrchestratorNilStore
// so the misconfiguration is loud, not silent.
func TestOrchestrator_NilStore_ReturnsError(t *testing.T) {
	o := &Orchestrator{Store: nil, Log: discardLog(), Actor: "meterd:safedeploy"}
	_, _, err := o.Once(context.Background())
	if !errors.Is(err, ErrOrchestratorNilStore) {
		t.Errorf("err = %v; want ErrOrchestratorNilStore", err)
	}
}

// TestOrchestrator_NoPendingRows_NoOp — an empty pending walk
// must return zero counters and a nil error.
func TestOrchestrator_NoPendingRows_NoOp(t *testing.T) {
	store := newStubStore()
	o := NewOrchestrator(store, discardLog(), "meterd:safedeploy", "")
	stats, _, err := o.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if stats.Started+stats.Completed+stats.StuckDetected+stats.AuditEmitFailed != 0 {
		t.Errorf("stats = %+v; want zero counters", stats)
	}
	if store.listCalls != 1 {
		t.Errorf("list calls = %d; want 1", store.listCalls)
	}
}

// TestSetStuckAfterDuration_EnvOverride pins the env-scoped
// stuck-after setter (production-leveling Stream C, mirror of
// pkg/state.TestRecoverRolloutStuckAfter_EnvOverride): a positive
// duration applies, zero + negative are silently ignored so a bad
// env parse never inverts the stuck predicate (which would mark
// every fresh rollout as stuck).
func TestSetStuckAfterDuration_EnvOverride(t *testing.T) {
	original := StuckAfterDuration
	t.Cleanup(func() { StuckAfterDuration = original })

	SetStuckAfterDuration(5 * time.Minute)
	if got := StuckAfterDuration; got != 5*time.Minute {
		t.Errorf("after SetStuckAfterDuration(5m) = %s; want 5m", got)
	}

	SetStuckAfterDuration(0)
	if got := StuckAfterDuration; got != 5*time.Minute {
		t.Errorf("after SetStuckAfterDuration(0) = %s; want 5m (zero must be ignored)", got)
	}

	SetStuckAfterDuration(-1 * time.Second)
	if got := StuckAfterDuration; got != 5*time.Minute {
		t.Errorf("after SetStuckAfterDuration(-1s) = %s; want 5m (negative must be ignored)", got)
	}
}

// TestOrchestrator_NilCanaryStepStartedAt_DefensiveGuard —
// code-review finding #2 hardening (migration 00517). Pre-00517
// canary_step_started_at was nullable, so a rolling_out row could
// legally have nil. The orchestrator's stuck-detection branch
// (`if d.CanaryStepStartedAt != nil`) silently skipped the check
// and fell through to the healthy-in-flight return with no log,
// no counter increment, and no operator visibility. Post-00517
// the column is NOT NULL DEFAULT NOW(), so this branch should
// never fire in steady state — but the defensive guard logs +
// bumps Stats.StuckCheckMissingTimestamp when it does, so a
// write path that bypasses the schema default surfaces loudly.
func TestOrchestrator_NilCanaryStepStartedAt_DefensiveGuard(t *testing.T) {
	store := newStubStore()
	dep := seedDeployment(store, t, func(d *state.Deployment) {
		d.RolloutState = "rolling_out"
		d.CanaryStep = 1
		d.CanaryTotalSteps = 4
		d.CanaryStepStartedAt = nil // the defensive-guard branch
		d.RolloutStartedAt = nil
	})
	o := NewOrchestrator(store, discardLog(), "meterd:safedeploy", "")

	stats, _, err := o.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if stats.StuckCheckMissingTimestamp != 1 {
		t.Errorf("stats.StuckCheckMissingTimestamp = %d; want 1 (rolling_out row with nil canary_step_started_at must bump the tripwire counter)", stats.StuckCheckMissingTimestamp)
	}
	if stats.StuckDetected != 0 {
		t.Errorf("stats.StuckDetected = %d; want 0 (no stuck detection — the nil branch is the separate defensive counter)", stats.StuckDetected)
	}
	if stats.Completed != 0 || stats.Started != 0 {
		t.Errorf("stats = %+v; want no transitions on nil-timestamp row", stats)
	}
	got := store.rollouts[dep.ID]
	if got.RolloutState != "rolling_out" {
		t.Errorf("rollout_state = %q; want rolling_out (orchestrator must not auto-recover on nil timestamp)", got.RolloutState)
	}
}
