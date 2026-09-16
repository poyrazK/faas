package sched

// adr: 171 — disposable one-shot execution lease, dispatch, and teardown invariants.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	dto "github.com/prometheus/client_model/go"
)

type executionBackendFunc func(context.Context, ExecutionRestoreRequest) (ExecutionSession, error)

func (f executionBackendFunc) Restore(ctx context.Context, request ExecutionRestoreRequest) (ExecutionSession, error) {
	return f(ctx, request)
}

type executionSessionFuncs struct {
	execute func(context.Context, ExecutionPayload) (ExecutionOutcome, error)
	destroy func(context.Context) error
}

func (s executionSessionFuncs) Execute(ctx context.Context, payload ExecutionPayload) (ExecutionOutcome, error) {
	return s.execute(ctx, payload)
}

func (s executionSessionFuncs) Destroy(ctx context.Context) error {
	return s.destroy(ctx)
}

type teardownCheckingExecutionStore struct {
	state.ExecutionStore
	destroyed *atomic.Bool
	complete  atomic.Int32
}

func (s *teardownCheckingExecutionStore) CompleteExecution(ctx context.Context, params state.CompleteExecutionParams) (state.Execution, error) {
	s.complete.Add(1)
	if !s.destroyed.Load() {
		return state.Execution{}, errors.New("terminalized before teardown")
	}
	return s.ExecutionStore.CompleteExecution(ctx, params)
}

type claimCountingExecutionStore struct {
	state.ExecutionStore
	claims atomic.Int32
}

type cancelOnFirstCompletionStore struct {
	state.ExecutionStore
	accountID string
	once      atomic.Bool
}

func (s *cancelOnFirstCompletionStore) CompleteExecution(ctx context.Context, params state.CompleteExecutionParams) (state.Execution, error) {
	if s.once.CompareAndSwap(false, true) {
		if _, err := s.ExecutionStore.RequestExecutionCancellation(ctx, s.accountID, params.ID, params.FinishedAt); err != nil {
			return state.Execution{}, err
		}
	}
	return s.ExecutionStore.CompleteExecution(ctx, params)
}

func (s *claimCountingExecutionStore) ClaimExecution(ctx context.Context, owner string, claimedAt time.Time, leaseDuration time.Duration) (state.ExecutionClaim, error) {
	s.claims.Add(1)
	return s.ExecutionStore.ClaimExecution(ctx, owner, claimedAt, leaseDuration)
}

func newExecutionCoordinatorFixture(t *testing.T, count, timeoutMS int) (*state.MemStore, state.Account, []state.Execution, []byte) {
	t.Helper()
	store := state.NewMemStore()
	account, err := store.CreateAccount(context.Background(), "execution-coordinator@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	source := "export default async function main(input) { return input }"
	input := []byte(`{"hello":"world"}`)
	request := api.CreateExecutionRequest{
		Runtime: api.ExecutionRuntimeNode22,
		Source:  source,
		Input:   input,
		Limits:  &api.ExecutionLimitRequest{TimeoutMS: timeoutMS},
	}
	resolved, problem := request.Resolve(api.PlanPro)
	if problem != nil {
		t.Fatalf("Resolve: %v", problem)
	}
	sealed := []byte("sealed-source-and-input")
	created := make([]state.Execution, 0, count)
	for i := 0; i < count; i++ {
		admittedAt := time.Now().UTC()
		row, createErr := store.CreateExecution(context.Background(), state.CreateExecutionParams{
			AccountID: account.ID, Request: resolved, SourceBytes: len(source), InputBytes: len(input),
			AdmittedAt: admittedAt, DeadlineAt: admittedAt.Add(time.Duration(timeoutMS) * time.Millisecond),
			SealedPayload: sealed, PayloadKID: "execution-test-key",
		})
		if createErr != nil {
			t.Fatalf("CreateExecution(%d): %v", i, createErr)
		}
		created = append(created, row)
	}
	return store, account, created, sealed
}

func executionCoordinatorTestConfig() ExecutionCoordinatorConfig {
	return ExecutionCoordinatorConfig{
		Owner: "schedd-test", MaxConcurrent: 1,
		LeaseDuration: 500 * time.Millisecond, LeaseRenewInterval: 20 * time.Millisecond,
		PollInterval: 5 * time.Millisecond, SweepInterval: 10 * time.Millisecond,
		DestroyTimeout: time.Second, FinalizeTimeout: time.Second, SweepLimit: 100,
	}
}

func TestExecutionCoordinatorConfigBoundsDispatchPool(t *testing.T) {
	config := normalizeExecutionCoordinatorConfig(ExecutionCoordinatorConfig{MaxConcurrent: 0})
	if config.MaxConcurrent != DefaultExecutionDispatchConcurrency {
		t.Fatalf("default MaxConcurrent = %d, want %d", config.MaxConcurrent, DefaultExecutionDispatchConcurrency)
	}
	config = normalizeExecutionCoordinatorConfig(ExecutionCoordinatorConfig{MaxConcurrent: MaxExecutionDispatchConcurrency + 1, QueueAccountLimit: 5000})
	if config.MaxConcurrent != MaxExecutionDispatchConcurrency || config.QueueAccountLimit != 1000 {
		t.Fatalf("bounded config = %+v, want max workers=%d/account limit=1000", config, MaxExecutionDispatchConcurrency)
	}
}

func TestExecutionCoordinatorFairClaimsAcrossAccounts(t *testing.T) {
	store := state.NewMemStore()
	accountA, err := store.CreateAccount(context.Background(), "fair-a@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount A: %v", err)
	}
	accountB, err := store.CreateAccount(context.Background(), "fair-b@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount B: %v", err)
	}
	request := api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "return input", Input: []byte(`{"ok":true}`)}
	resolved, problem := request.Resolve(api.PlanPro)
	if problem != nil {
		t.Fatalf("Resolve: %v", problem)
	}
	base := time.Now().UTC().Add(-time.Second)
	for i, accountID := range []string{accountA.ID, accountA.ID, accountB.ID} {
		admittedAt := base.Add(time.Duration(i) * time.Millisecond)
		_, err := store.CreateExecution(context.Background(), state.CreateExecutionParams{
			AccountID: accountID, Request: resolved, SourceBytes: len(request.Source), InputBytes: len(request.Input),
			AdmittedAt: admittedAt, DeadlineAt: admittedAt.Add(time.Duration(resolved.Limits.TimeoutMS) * time.Millisecond),
			SealedPayload: []byte("sealed"), PayloadKID: "kid",
		})
		if err != nil {
			t.Fatalf("CreateExecution(%d): %v", i, err)
		}
	}
	var claimed []string
	backend := executionBackendFunc(func(_ context.Context, req ExecutionRestoreRequest) (ExecutionSession, error) {
		claimed = append(claimed, req.AccountID)
		return executionSessionFuncs{
			execute: func(context.Context, ExecutionPayload) (ExecutionOutcome, error) {
				return ExecutionOutcome{Status: api.ExecutionStatusSucceeded}, nil
			},
			destroy: func(context.Context) error { return nil },
		}, nil
	})
	coordinator := NewExecutionCoordinator(store, backend, executionCoordinatorTestConfig(), nil)
	for i := 0; i < 2; i++ {
		if processed, err := coordinator.ProcessNext(context.Background()); err != nil || !processed {
			t.Fatalf("ProcessNext(%d) = %v, %v", i, processed, err)
		}
	}
	if len(claimed) != 2 || claimed[0] == claimed[1] {
		t.Fatalf("claimed account order = %v, want two distinct accounts", claimed)
	}
}

func TestExecutionCoordinatorDispatchFenceAndTeardownBeforeCompletion(t *testing.T) {
	store, account, executions, sealed := newExecutionCoordinatorFixture(t, 1, 2000)
	destroyed := &atomic.Bool{}
	checkingStore := &teardownCheckingExecutionStore{ExecutionStore: store, destroyed: destroyed}
	backend := executionBackendFunc(func(_ context.Context, request ExecutionRestoreRequest) (ExecutionSession, error) {
		if request.ID != executions[0].ID || request.AccountID != account.ID || request.Runtime != api.ExecutionRuntimeNode22 {
			return nil, fmt.Errorf("unexpected restore request: %#v", request)
		}
		return executionSessionFuncs{
			execute: func(ctx context.Context, payload ExecutionPayload) (ExecutionOutcome, error) {
				row, err := store.ExecutionByID(ctx, account.ID, executions[0].ID)
				if err != nil {
					return ExecutionOutcome{}, err
				}
				if row.Status != api.ExecutionStatusRunning {
					return ExecutionOutcome{}, fmt.Errorf("payload dispatched before running fence: %s", row.Status)
				}
				if string(payload.Sealed) != string(sealed) || payload.KID != "execution-test-key" {
					return ExecutionOutcome{}, fmt.Errorf("unexpected payload: %q/%q", payload.Sealed, payload.KID)
				}
				exitCode := 0
				return ExecutionOutcome{
					Status: api.ExecutionStatusSucceeded, Result: []byte(`{"ok":true}`), Stdout: "ok\n",
					ExitCode: &exitCode, Usage: api.ExecutionUsage{WallTimeMS: 4, CPUTimeMS: 2, PeakMemoryMB: 12},
				}, nil
			},
			destroy: func(context.Context) error {
				destroyed.Store(true)
				return nil
			},
		}, nil
	})
	coordinator := NewExecutionCoordinator(checkingStore, backend, executionCoordinatorTestConfig(), nil)

	processed, err := coordinator.ProcessNext(context.Background())
	if err != nil || !processed {
		t.Fatalf("ProcessNext = %v, %v", processed, err)
	}
	row, err := store.ExecutionByID(context.Background(), account.ID, executions[0].ID)
	if err != nil {
		t.Fatalf("ExecutionByID: %v", err)
	}
	if row.Status != api.ExecutionStatusSucceeded || string(row.Result) != `{"ok":true}` || !destroyed.Load() {
		t.Fatalf("completed execution = %#v, destroyed=%v", row, destroyed.Load())
	}
	if checkingStore.complete.Load() != 1 {
		t.Fatalf("CompleteExecution calls = %d, want 1", checkingStore.complete.Load())
	}
}

func TestExecutionCoordinatorEmitsBoundedLifecycleMetrics(t *testing.T) {
	store, account, executions, _ := newExecutionCoordinatorFixture(t, 1, 2000)
	ops := wire.NewOpsMetrics("schedd")
	backend := executionBackendFunc(func(context.Context, ExecutionRestoreRequest) (ExecutionSession, error) {
		return executionSessionFuncs{
			execute: func(context.Context, ExecutionPayload) (ExecutionOutcome, error) {
				return ExecutionOutcome{
					Status: api.ExecutionStatusSucceeded, Result: []byte(`{"ok":true}`),
					Stdout: "ok\n", Stderr: "warning\n",
				}, nil
			},
			destroy: func(context.Context) error { return nil },
		}, nil
	})
	config := executionCoordinatorTestConfig()
	config.Metrics = ops
	coordinator := NewExecutionCoordinator(store, backend, config, nil)
	coordinator.observeQueuePressure(context.Background())
	if got := executionMetricValue(t, ops, "schedd_execution_queue_depth", nil); got != 1 {
		t.Fatalf("queue depth = %v, want 1", got)
	}
	if got := executionMetricValue(t, ops, "schedd_execution_queue_oldest_wait_seconds", nil); got < 0 {
		t.Fatalf("queue oldest wait = %v, want non-negative", got)
	}

	if processed, err := coordinator.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("ProcessNext = %v, %v", processed, err)
	}
	if got := executionMetricValue(t, ops, "schedd_execution_active", map[string]string{"runtime": "node22"}); got != 0 {
		t.Fatalf("active executions = %v, want 0 after teardown", got)
	}
	if got := executionMetricValue(t, ops, "schedd_execution_total", map[string]string{"runtime": "node22", "status": "succeeded"}); got != 1 {
		t.Fatalf("terminal executions = %v, want 1", got)
	}
	if got := executionMetricValue(t, ops, "schedd_execution_output_bytes_total", map[string]string{"runtime": "node22"}); got != float64(len(`{"ok":true}`)+len("ok\n")+len("warning\n")) {
		t.Fatalf("output bytes = %v, want %d", got, len(`{"ok":true}`)+len("ok\n")+len("warning\n"))
	}
	for _, phase := range []string{"restore", "execute", "teardown", "finalize"} {
		if got := executionMetricHistogramCount(t, ops, "schedd_execution_phase_duration_seconds", map[string]string{"runtime": "node22", "phase": phase}); got != 1 {
			t.Fatalf("%s phase observations = %d, want 1", phase, got)
		}
	}
	if got := executionMetricValue(t, ops, "schedd_execution_failures_total", map[string]string{"runtime": "node22", "reason": "unknown"}); got != 0 {
		t.Fatalf("unknown failures = %v, want 0", got)
	}
	row, err := store.ExecutionByID(context.Background(), account.ID, executions[0].ID)
	if err != nil || row.Status != api.ExecutionStatusSucceeded {
		t.Fatalf("execution row = %#v, err=%v", row, err)
	}
}

func TestExecutionCoordinatorLogsNoBackendPayloadAndUsesBoundedFailureLabels(t *testing.T) {
	store, account, executions, _ := newExecutionCoordinatorFixture(t, 1, 2000)
	ops := wire.NewOpsMetrics("schedd")
	var logBuffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuffer, nil))
	backend := executionBackendFunc(func(context.Context, ExecutionRestoreRequest) (ExecutionSession, error) {
		return nil, errors.New("payload=TOP_SECRET_SOURCE input=TOP_SECRET_INPUT")
	})
	config := executionCoordinatorTestConfig()
	config.Metrics = ops
	coordinator := NewExecutionCoordinator(store, backend, config, logger)

	if processed, err := coordinator.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("ProcessNext = %v, %v", processed, err)
	}
	if strings.Contains(logBuffer.String(), "TOP_SECRET") {
		t.Fatalf("backend payload leaked into log: %s", logBuffer.String())
	}
	if got := executionMetricValue(t, ops, "schedd_execution_failures_total", map[string]string{"runtime": "node22", "reason": "restore"}); got != 1 {
		t.Fatalf("restore failures = %v, want 1", got)
	}
	if got := executionMetricValue(t, ops, "schedd_execution_total", map[string]string{"runtime": "node22", "status": "failed"}); got != 1 {
		t.Fatalf("failed executions = %v, want 1", got)
	}
	if got := executionMetricValue(t, ops, "schedd_execution_active", map[string]string{"runtime": "node22"}); got != 0 {
		t.Fatalf("active executions = %v, want 0", got)
	}
	row, err := store.ExecutionByID(context.Background(), account.ID, executions[0].ID)
	if err != nil || row.FailureCode == nil || *row.FailureCode != "restore_failed" {
		t.Fatalf("execution failure row = %#v, err=%v", row, err)
	}
}

func executionMetricValue(t *testing.T, metrics *wire.OpsMetrics, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := metrics.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if !executionMetricLabelsMatch(metric.GetLabel(), labels) {
				continue
			}
			if metric.Counter != nil {
				return metric.GetCounter().GetValue()
			}
			if metric.Gauge != nil {
				return metric.GetGauge().GetValue()
			}
		}
	}
	t.Fatalf("metric %s with labels %#v not found", name, labels)
	return 0
}

func executionMetricHistogramCount(t *testing.T, metrics *wire.OpsMetrics, name string, labels map[string]string) uint64 {
	t.Helper()
	families, err := metrics.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if executionMetricLabelsMatch(metric.GetLabel(), labels) {
				return metric.GetHistogram().GetSampleCount()
			}
		}
	}
	t.Fatalf("metric %s with labels %#v not found", name, labels)
	return 0
}

func executionMetricLabelsMatch(labels []*dto.LabelPair, want map[string]string) bool {
	if len(labels) != len(want) {
		return false
	}
	for _, label := range labels {
		if want[label.GetName()] != label.GetValue() {
			return false
		}
	}
	return true
}

func TestExecutionCoordinatorCancellationStopsGuestAndAcknowledgesAfterTeardown(t *testing.T) {
	store, account, executions, _ := newExecutionCoordinatorFixture(t, 1, 2000)
	destroyed := &atomic.Bool{}
	checkingStore := &teardownCheckingExecutionStore{ExecutionStore: store, destroyed: destroyed}
	started := make(chan struct{})
	backend := executionBackendFunc(func(context.Context, ExecutionRestoreRequest) (ExecutionSession, error) {
		return executionSessionFuncs{
			execute: func(ctx context.Context, _ ExecutionPayload) (ExecutionOutcome, error) {
				close(started)
				<-ctx.Done()
				return ExecutionOutcome{}, ctx.Err()
			},
			destroy: func(context.Context) error {
				destroyed.Store(true)
				return nil
			},
		}, nil
	})
	config := executionCoordinatorTestConfig()
	config.LeaseRenewInterval = 5 * time.Millisecond
	coordinator := NewExecutionCoordinator(checkingStore, backend, config, nil)
	processCtx, cancelProcess := context.WithCancel(context.Background())
	t.Cleanup(cancelProcess)
	done := make(chan error, 1)
	go func() {
		_, err := coordinator.ProcessNext(processCtx)
		done <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("execution did not start")
	}
	if _, err := store.RequestExecutionCancellation(context.Background(), account.ID, executions[0].ID, time.Now().UTC()); err != nil {
		t.Fatalf("RequestExecutionCancellation: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ProcessNext: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled execution did not stop")
	}
	row, err := store.ExecutionByID(context.Background(), account.ID, executions[0].ID)
	if err != nil {
		t.Fatalf("ExecutionByID: %v", err)
	}
	if row.Status != api.ExecutionStatusCancelled || !destroyed.Load() || checkingStore.complete.Load() != 1 {
		t.Fatalf("cancelled execution = %#v, destroyed=%v completes=%d", row, destroyed.Load(), checkingStore.complete.Load())
	}
}

func TestExecutionCoordinatorDeadlineDestroysBeforeDurableSweep(t *testing.T) {
	store, account, executions, _ := newExecutionCoordinatorFixture(t, 1, 100)
	destroyed := &atomic.Bool{}
	backend := executionBackendFunc(func(context.Context, ExecutionRestoreRequest) (ExecutionSession, error) {
		return executionSessionFuncs{
			execute: func(ctx context.Context, _ ExecutionPayload) (ExecutionOutcome, error) {
				<-ctx.Done()
				return ExecutionOutcome{}, ctx.Err()
			},
			destroy: func(context.Context) error {
				destroyed.Store(true)
				return nil
			},
		}, nil
	})
	coordinator := NewExecutionCoordinator(store, backend, executionCoordinatorTestConfig(), nil)

	processed, err := coordinator.ProcessNext(context.Background())
	if err != nil || !processed {
		t.Fatalf("ProcessNext = %v, %v", processed, err)
	}
	row, err := store.ExecutionByID(context.Background(), account.ID, executions[0].ID)
	if err != nil {
		t.Fatalf("ExecutionByID: %v", err)
	}
	if row.Status != api.ExecutionStatusTimedOut || !destroyed.Load() {
		t.Fatalf("deadline execution = %#v, destroyed=%v", row, destroyed.Load())
	}
}

func TestExecutionCoordinatorCancellationRaceOverridesGuestSuccess(t *testing.T) {
	store, account, executions, _ := newExecutionCoordinatorFixture(t, 1, 2000)
	racingStore := &cancelOnFirstCompletionStore{ExecutionStore: store, accountID: account.ID}
	backend := executionBackendFunc(func(context.Context, ExecutionRestoreRequest) (ExecutionSession, error) {
		return executionSessionFuncs{
			execute: func(context.Context, ExecutionPayload) (ExecutionOutcome, error) {
				return ExecutionOutcome{Status: api.ExecutionStatusSucceeded, Result: []byte(`null`)}, nil
			},
			destroy: func(context.Context) error { return nil },
		}, nil
	})
	coordinator := NewExecutionCoordinator(racingStore, backend, executionCoordinatorTestConfig(), nil)

	processed, err := coordinator.ProcessNext(context.Background())
	if err != nil || !processed {
		t.Fatalf("ProcessNext = %v, %v", processed, err)
	}
	row, err := store.ExecutionByID(context.Background(), account.ID, executions[0].ID)
	if err != nil {
		t.Fatalf("ExecutionByID: %v", err)
	}
	if row.Status != api.ExecutionStatusCancelled || row.Result != nil {
		t.Fatalf("completion race result = %#v", row)
	}
}

func TestExecutionCoordinatorWithholdsTerminalStateWhenTeardownFails(t *testing.T) {
	store, account, executions, _ := newExecutionCoordinatorFixture(t, 1, 2000)
	backend := executionBackendFunc(func(context.Context, ExecutionRestoreRequest) (ExecutionSession, error) {
		return executionSessionFuncs{
			execute: func(context.Context, ExecutionPayload) (ExecutionOutcome, error) {
				return ExecutionOutcome{Status: api.ExecutionStatusSucceeded, Result: []byte(`null`)}, nil
			},
			destroy: func(context.Context) error { return errors.New("jail still busy") },
		}, nil
	})
	config := executionCoordinatorTestConfig()
	config.LeaseDuration = 100 * time.Millisecond
	config.LeaseRenewInterval = 25 * time.Millisecond
	coordinator := NewExecutionCoordinator(store, backend, config, nil)

	processed, err := coordinator.ProcessNext(context.Background())
	if !processed || err == nil || !strings.Contains(err.Error(), "jail still busy") {
		t.Fatalf("ProcessNext = %v, %v", processed, err)
	}
	row, readErr := store.ExecutionByID(context.Background(), account.ID, executions[0].ID)
	if readErr != nil {
		t.Fatalf("ExecutionByID: %v", readErr)
	}
	if row.Status != api.ExecutionStatusRunning {
		t.Fatalf("status after teardown failure = %s, want running", row.Status)
	}
	coordinator.now = func() time.Time { return time.Now().UTC().Add(500 * time.Millisecond) }
	if _, sweepErr := coordinator.SweepOnce(context.Background()); sweepErr != nil {
		t.Fatalf("SweepOnce: %v", sweepErr)
	}
	row, _ = store.ExecutionByID(context.Background(), account.ID, executions[0].ID)
	if row.Status != api.ExecutionStatusFailed || row.FailureCode == nil || *row.FailureCode != "lease_expired" {
		t.Fatalf("recovered execution = %#v", row)
	}
}

func TestExecutionCoordinatorBoundsConcurrentClaims(t *testing.T) {
	store, account, executions, _ := newExecutionCoordinatorFixture(t, 3, 2000)
	started := make(chan string, len(executions))
	release := make(chan struct{}, len(executions))
	var active atomic.Int32
	var maximum atomic.Int32
	backend := executionBackendFunc(func(_ context.Context, request ExecutionRestoreRequest) (ExecutionSession, error) {
		return executionSessionFuncs{
			execute: func(ctx context.Context, _ ExecutionPayload) (ExecutionOutcome, error) {
				current := active.Add(1)
				defer active.Add(-1)
				for {
					seen := maximum.Load()
					if current <= seen || maximum.CompareAndSwap(seen, current) {
						break
					}
				}
				started <- request.ID
				select {
				case <-ctx.Done():
					return ExecutionOutcome{}, ctx.Err()
				case <-release:
					return ExecutionOutcome{Status: api.ExecutionStatusSucceeded, Result: []byte(`null`)}, nil
				}
			},
			destroy: func(context.Context) error { return nil },
		}, nil
	})
	config := executionCoordinatorTestConfig()
	config.Enabled = true
	config.MaxConcurrent = 2
	coordinator := NewExecutionCoordinator(store, backend, config, nil)
	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runDone := make(chan error, 1)
	go func() { runDone <- coordinator.Run(runCtx) }()

	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatalf("execution %d did not start", i)
		}
	}
	select {
	case id := <-started:
		t.Fatalf("third execution %s started while both slots were occupied", id)
	case <-time.After(40 * time.Millisecond):
	}
	release <- struct{}{}
	release <- struct{}{}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("third execution did not start after a slot was released")
	}
	release <- struct{}{}

	eventually(t, time.Second, func() bool {
		for _, execution := range executions {
			row, err := store.ExecutionByID(context.Background(), account.ID, execution.ID)
			if err != nil || row.Status != api.ExecutionStatusSucceeded {
				return false
			}
		}
		return true
	})
	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not stop")
	}
	if maximum.Load() != 2 {
		t.Fatalf("maximum concurrent executions = %d, want 2", maximum.Load())
	}
}

func TestExecutionCoordinatorRunIsDisabledByDefault(t *testing.T) {
	store, _, _, _ := newExecutionCoordinatorFixture(t, 1, 2000)
	counting := &claimCountingExecutionStore{ExecutionStore: store}
	backend := executionBackendFunc(func(context.Context, ExecutionRestoreRequest) (ExecutionSession, error) {
		return nil, errors.New("must not be called")
	})
	coordinator := NewExecutionCoordinator(counting, backend, ExecutionCoordinatorConfig{}, nil)
	if err := coordinator.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if counting.claims.Load() != 0 {
		t.Fatalf("claims = %d, want 0", counting.claims.Load())
	}
}

func eventually(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition did not become true before timeout")
}

func TestNormalizeExecutionOutcomeFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		outcome ExecutionOutcome
		code    string
	}{
		{name: "invalid JSON", outcome: ExecutionOutcome{Status: api.ExecutionStatusSucceeded, Result: []byte(`{`)}, code: "guest_protocol_error"},
		{name: "oversized output", outcome: ExecutionOutcome{Status: api.ExecutionStatusSucceeded, Stdout: "12345"}, code: "output_limit_exceeded"},
		{name: "untrusted status", outcome: ExecutionOutcome{Status: api.ExecutionStatusCancelled}, code: "guest_protocol_error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := normalizeExecutionOutcome(test.outcome, 4)
			if got.Status != api.ExecutionStatusFailed || got.FailureCode != test.code {
				t.Fatalf("normalizeExecutionOutcome = %#v", got)
			}
		})
	}
}
