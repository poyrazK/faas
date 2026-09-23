package sched

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type appTaskBackendFunc func(context.Context, AppTaskRestoreRequest) (AppTaskSession, error)

func (f appTaskBackendFunc) Restore(ctx context.Context, request AppTaskRestoreRequest) (AppTaskSession, error) {
	return f(ctx, request)
}

type appTaskSessionFuncs struct {
	execute func(context.Context, AppTaskExecuteRequest) (AppTaskOutcome, error)
	destroy func(context.Context) error
}

func (s appTaskSessionFuncs) Execute(ctx context.Context, request AppTaskExecuteRequest) (AppTaskOutcome, error) {
	return s.execute(ctx, request)
}

func (s appTaskSessionFuncs) Destroy(ctx context.Context) error {
	return s.destroy(ctx)
}

type teardownCheckingAppTaskStore struct {
	state.AppTaskStore
	destroyed *atomic.Bool
	complete  atomic.Int32
}

func (s *teardownCheckingAppTaskStore) CompleteAppTask(ctx context.Context, params state.CompleteAppTaskParams) (state.AppTask, error) {
	s.complete.Add(1)
	if !s.destroyed.Load() {
		return state.AppTask{}, errors.New("terminalized before teardown")
	}
	return s.AppTaskStore.CompleteAppTask(ctx, params)
}

type cancelOnFirstAppTaskCompletionStore struct {
	state.AppTaskStore
	accountID string
	appID     string
	once      atomic.Bool
}

func (s *cancelOnFirstAppTaskCompletionStore) CompleteAppTask(ctx context.Context, params state.CompleteAppTaskParams) (state.AppTask, error) {
	if s.once.CompareAndSwap(false, true) {
		if _, err := s.AppTaskStore.RequestAppTaskCancellation(ctx, s.accountID, s.appID, params.ID, params.FinishedAt); err != nil {
			return state.AppTask{}, err
		}
	}
	return s.AppTaskStore.CompleteAppTask(ctx, params)
}

type claimCountingAppTaskStore struct {
	state.AppTaskStore
	claims atomic.Int32
}

func (s *claimCountingAppTaskStore) ClaimNextAppTask(ctx context.Context, owner string, claimedAt time.Time, leaseDuration time.Duration) (state.AppTask, error) {
	s.claims.Add(1)
	return s.AppTaskStore.ClaimNextAppTask(ctx, owner, claimedAt, leaseDuration)
}

func newAppTaskCoordinatorFixture(t *testing.T, count, timeoutSeconds, maxOutputBytes int) (*state.MemStore, state.Account, state.App, state.Deployment, []state.AppTask) {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "app-task-coordinator@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	limits := api.MustLimitsFor(api.PlanPro)
	app, err := store.CreateAppIfUnderQuota(ctx, state.App{
		AccountID: account.ID, Slug: "app-task-coordinator", Type: state.AppTypeApp,
		Runtime: "node22", RAMMB: limits.RAMMB, MaxConcurrency: limits.MaxConcurrency, IdleTimeoutS: limits.IdleTimeoutS,
	}, limits)
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota: %v", err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:app-task-coordinator", Status: state.DeployLive,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/tmp/app-task.ext4", "apps/app-task/deployment.ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs: %v", err)
	}
	deployment, err = store.DeploymentByID(ctx, deployment.ID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	created := make([]state.AppTask, 0, count)
	base := time.Now().UTC().Add(-time.Second)
	for i := 0; i < count; i++ {
		task, createErr := store.CreateAppTask(ctx, state.CreateAppTaskParams{
			AccountID: account.ID, AppID: app.ID, DeploymentID: deployment.ID,
			Kind: state.AppTaskKindManual, Command: []string{"bin/migrate", fmt.Sprintf("--shard=%d", i)},
			TimeoutSeconds: timeoutSeconds, MaxOutputBytes: maxOutputBytes,
			CreatedAt: base.Add(time.Duration(i) * time.Millisecond),
		})
		if createErr != nil {
			t.Fatalf("CreateAppTask(%d): %v", i, createErr)
		}
		created = append(created, task)
	}
	return store, account, app, deployment, created
}

func appTaskCoordinatorTestConfig() AppTaskCoordinatorConfig {
	return AppTaskCoordinatorConfig{
		Owner: "schedd-test", MaxConcurrent: 1,
		LeaseDuration: 100 * time.Millisecond, LeaseRenewInterval: 10 * time.Millisecond,
		PollInterval: 5 * time.Millisecond, SweepInterval: 10 * time.Millisecond,
		RestoreTimeout: time.Second, DestroyTimeout: time.Second, FinalizeTimeout: time.Second,
	}
}

func TestAppTaskCoordinatorConfigBoundsDispatchPool(t *testing.T) {
	config := normalizeAppTaskCoordinatorConfig(AppTaskCoordinatorConfig{MaxConcurrent: 0})
	if config.MaxConcurrent != DefaultAppTaskDispatchConcurrency {
		t.Fatalf("default MaxConcurrent = %d, want %d", config.MaxConcurrent, DefaultAppTaskDispatchConcurrency)
	}
	config = normalizeAppTaskCoordinatorConfig(AppTaskCoordinatorConfig{MaxConcurrent: MaxAppTaskDispatchConcurrency + 1})
	if config.MaxConcurrent != MaxAppTaskDispatchConcurrency {
		t.Fatalf("bounded MaxConcurrent = %d, want %d", config.MaxConcurrent, MaxAppTaskDispatchConcurrency)
	}
}

func TestAppTaskCoordinatorDispatchFenceRenewsLeaseAndDestroysBeforeCompletion(t *testing.T) {
	store, account, app, deployment, tasks := newAppTaskCoordinatorFixture(t, 1, 10, 2048)
	destroyed := &atomic.Bool{}
	checkingStore := &teardownCheckingAppTaskStore{AppTaskStore: store, destroyed: destroyed}
	backend := appTaskBackendFunc(func(_ context.Context, request AppTaskRestoreRequest) (AppTaskSession, error) {
		if request.ID != tasks[0].ID || request.AccountID != account.ID || request.AppID != app.ID ||
			request.DeploymentID != deployment.ID || request.Kind != state.AppTaskKindManual ||
			request.ArtifactKey != deployment.RootfsKey || request.ImageDigest != deployment.ImageDigest {
			return nil, fmt.Errorf("unexpected restore request: %#v", request)
		}
		return appTaskSessionFuncs{
			execute: func(ctx context.Context, request AppTaskExecuteRequest) (AppTaskOutcome, error) {
				row, err := store.AppTaskByID(ctx, account.ID, app.ID, tasks[0].ID)
				if err != nil {
					return AppTaskOutcome{}, err
				}
				if row.Status != state.AppTaskRunning {
					return AppTaskOutcome{}, fmt.Errorf("command dispatched before running fence: %s", row.Status)
				}
				if strings.Join(request.Command, " ") != "bin/migrate --shard=0" || request.CommandShell || request.Timeout != 10*time.Second {
					return AppTaskOutcome{}, fmt.Errorf("unexpected execute request: %#v", request)
				}
				// Exceed the initial lease so success proves the monitor renewed it.
				time.Sleep(140 * time.Millisecond)
				exitCode := 0
				return AppTaskOutcome{Status: state.AppTaskSucceeded, StdoutTail: "migrated\n", ExitCode: &exitCode}, nil
			},
			destroy: func(context.Context) error {
				destroyed.Store(true)
				return nil
			},
		}, nil
	})
	coordinator := NewAppTaskCoordinator(checkingStore, backend, appTaskCoordinatorTestConfig(), nil)

	processed, err := coordinator.ProcessNext(context.Background())
	if err != nil || !processed {
		t.Fatalf("ProcessNext = %v, %v", processed, err)
	}
	row, err := store.AppTaskByID(context.Background(), account.ID, app.ID, tasks[0].ID)
	if err != nil || row.Status != state.AppTaskSucceeded || row.StdoutTail != "migrated\n" || !destroyed.Load() {
		t.Fatalf("completed task = %#v, destroyed=%v, err=%v", row, destroyed.Load(), err)
	}
	if checkingStore.complete.Load() != 1 {
		t.Fatalf("CompleteAppTask calls = %d, want 1", checkingStore.complete.Load())
	}
}

func TestAppTaskCoordinatorCancellationStopsCommandAndAcknowledgesAfterTeardown(t *testing.T) {
	store, account, app, _, tasks := newAppTaskCoordinatorFixture(t, 1, 10, 2048)
	destroyed := &atomic.Bool{}
	checkingStore := &teardownCheckingAppTaskStore{AppTaskStore: store, destroyed: destroyed}
	started := make(chan struct{})
	backend := appTaskBackendFunc(func(context.Context, AppTaskRestoreRequest) (AppTaskSession, error) {
		return appTaskSessionFuncs{
			execute: func(ctx context.Context, _ AppTaskExecuteRequest) (AppTaskOutcome, error) {
				close(started)
				<-ctx.Done()
				return AppTaskOutcome{}, ctx.Err()
			},
			destroy: func(context.Context) error {
				destroyed.Store(true)
				return nil
			},
		}, nil
	})
	config := appTaskCoordinatorTestConfig()
	config.LeaseRenewInterval = 5 * time.Millisecond
	coordinator := NewAppTaskCoordinator(checkingStore, backend, config, nil)
	done := make(chan error, 1)
	go func() {
		_, err := coordinator.ProcessNext(context.Background())
		done <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("app task did not start")
	}
	if _, err := store.RequestAppTaskCancellation(context.Background(), account.ID, app.ID, tasks[0].ID, time.Now().UTC()); err != nil {
		t.Fatalf("RequestAppTaskCancellation: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ProcessNext: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled app task did not stop")
	}
	row, err := store.AppTaskByID(context.Background(), account.ID, app.ID, tasks[0].ID)
	if err != nil || row.Status != state.AppTaskCancelled || !destroyed.Load() || checkingStore.complete.Load() != 1 {
		t.Fatalf("cancelled task = %#v, destroyed=%v, completes=%d, err=%v", row, destroyed.Load(), checkingStore.complete.Load(), err)
	}
}

func TestAppTaskCoordinatorCancellationRaceOverridesSuccess(t *testing.T) {
	store, account, app, _, tasks := newAppTaskCoordinatorFixture(t, 1, 10, 2048)
	racingStore := &cancelOnFirstAppTaskCompletionStore{AppTaskStore: store, accountID: account.ID, appID: app.ID}
	backend := appTaskBackendFunc(func(context.Context, AppTaskRestoreRequest) (AppTaskSession, error) {
		return appTaskSessionFuncs{
			execute: func(context.Context, AppTaskExecuteRequest) (AppTaskOutcome, error) {
				exitCode := 0
				return AppTaskOutcome{Status: state.AppTaskSucceeded, StdoutTail: "done\n", ExitCode: &exitCode}, nil
			},
			destroy: func(context.Context) error { return nil },
		}, nil
	})
	coordinator := NewAppTaskCoordinator(racingStore, backend, appTaskCoordinatorTestConfig(), nil)

	if processed, err := coordinator.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("ProcessNext = %v, %v", processed, err)
	}
	row, err := store.AppTaskByID(context.Background(), account.ID, app.ID, tasks[0].ID)
	if err != nil || row.Status != state.AppTaskCancelled || row.StdoutTail != "" {
		t.Fatalf("completion race result = %#v, err=%v", row, err)
	}
}

func TestAppTaskCoordinatorTimeoutDestroysBeforeCompletion(t *testing.T) {
	store, account, app, _, tasks := newAppTaskCoordinatorFixture(t, 1, 1, 2048)
	destroyed := &atomic.Bool{}
	backend := appTaskBackendFunc(func(context.Context, AppTaskRestoreRequest) (AppTaskSession, error) {
		return appTaskSessionFuncs{
			execute: func(ctx context.Context, _ AppTaskExecuteRequest) (AppTaskOutcome, error) {
				<-ctx.Done()
				return AppTaskOutcome{}, ctx.Err()
			},
			destroy: func(context.Context) error {
				destroyed.Store(true)
				return nil
			},
		}, nil
	})
	coordinator := NewAppTaskCoordinator(store, backend, appTaskCoordinatorTestConfig(), nil)

	if processed, err := coordinator.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("ProcessNext = %v, %v", processed, err)
	}
	row, err := store.AppTaskByID(context.Background(), account.ID, app.ID, tasks[0].ID)
	if err != nil || row.Status != state.AppTaskTimedOut || row.FailureCode == nil || *row.FailureCode != "timeout" || !destroyed.Load() {
		t.Fatalf("timed out task = %#v, destroyed=%v, err=%v", row, destroyed.Load(), err)
	}
}

func TestAppTaskCoordinatorBoundsPersistedOutputAsTails(t *testing.T) {
	store, account, app, _, tasks := newAppTaskCoordinatorFixture(t, 1, 10, 1024)
	stdout := strings.Repeat("o", 900) + "stdout-tail"
	stderr := strings.Repeat("e", 900) + "stderr-tail"
	backend := appTaskBackendFunc(func(context.Context, AppTaskRestoreRequest) (AppTaskSession, error) {
		return appTaskSessionFuncs{
			execute: func(context.Context, AppTaskExecuteRequest) (AppTaskOutcome, error) {
				exitCode := 0
				return AppTaskOutcome{Status: state.AppTaskSucceeded, StdoutTail: stdout, StderrTail: stderr, ExitCode: &exitCode}, nil
			},
			destroy: func(context.Context) error { return nil },
		}, nil
	})
	coordinator := NewAppTaskCoordinator(store, backend, appTaskCoordinatorTestConfig(), nil)

	if processed, err := coordinator.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("ProcessNext = %v, %v", processed, err)
	}
	row, err := store.AppTaskByID(context.Background(), account.ID, app.ID, tasks[0].ID)
	if err != nil || row.Status != state.AppTaskSucceeded || !row.OutputTruncated || len(row.StdoutTail)+len(row.StderrTail) > 1024 ||
		!strings.HasSuffix(row.StdoutTail, "stdout-tail") || !strings.HasSuffix(row.StderrTail, "stderr-tail") {
		t.Fatalf("bounded task output = stdout:%d stderr:%d truncated:%v status:%s err=%v", len(row.StdoutTail), len(row.StderrTail), row.OutputTruncated, row.Status, err)
	}
}

func TestAppTaskCoordinatorWithholdsTerminalStateWhenTeardownFails(t *testing.T) {
	store, account, app, _, tasks := newAppTaskCoordinatorFixture(t, 1, 10, 2048)
	backend := appTaskBackendFunc(func(context.Context, AppTaskRestoreRequest) (AppTaskSession, error) {
		return appTaskSessionFuncs{
			execute: func(context.Context, AppTaskExecuteRequest) (AppTaskOutcome, error) {
				exitCode := 0
				return AppTaskOutcome{Status: state.AppTaskSucceeded, ExitCode: &exitCode}, nil
			},
			destroy: func(context.Context) error { return errors.New("jail still busy") },
		}, nil
	})
	config := appTaskCoordinatorTestConfig()
	config.LeaseDuration = 100 * time.Millisecond
	coordinator := NewAppTaskCoordinator(store, backend, config, nil)

	processed, err := coordinator.ProcessNext(context.Background())
	if !processed || err == nil || !strings.Contains(err.Error(), "jail still busy") {
		t.Fatalf("ProcessNext = %v, %v", processed, err)
	}
	row, readErr := store.AppTaskByID(context.Background(), account.ID, app.ID, tasks[0].ID)
	if readErr != nil || row.Status != state.AppTaskRunning {
		t.Fatalf("status after teardown failure = %#v, err=%v", row, readErr)
	}
	coordinator.now = func() time.Time { return time.Now().UTC().Add(time.Second) }
	if _, sweepErr := coordinator.SweepOnce(context.Background()); sweepErr != nil {
		t.Fatalf("SweepOnce: %v", sweepErr)
	}
	row, _ = store.AppTaskByID(context.Background(), account.ID, app.ID, tasks[0].ID)
	if row.Status != state.AppTaskFailed || row.FailureCode == nil || *row.FailureCode != "lease_expired" {
		t.Fatalf("recovered task = %#v", row)
	}
}

func TestAppTaskCoordinatorBoundsConcurrentClaims(t *testing.T) {
	store, account, app, _, tasks := newAppTaskCoordinatorFixture(t, 3, 10, 2048)
	started := make(chan string, len(tasks))
	release := make(chan struct{}, len(tasks))
	var active atomic.Int32
	var maximum atomic.Int32
	backend := appTaskBackendFunc(func(_ context.Context, request AppTaskRestoreRequest) (AppTaskSession, error) {
		return appTaskSessionFuncs{
			execute: func(ctx context.Context, _ AppTaskExecuteRequest) (AppTaskOutcome, error) {
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
					return AppTaskOutcome{}, ctx.Err()
				case <-release:
					exitCode := 0
					return AppTaskOutcome{Status: state.AppTaskSucceeded, ExitCode: &exitCode}, nil
				}
			},
			destroy: func(context.Context) error { return nil },
		}, nil
	})
	config := appTaskCoordinatorTestConfig()
	config.Enabled = true
	config.MaxConcurrent = 2
	coordinator := NewAppTaskCoordinator(store, backend, config, nil)
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- coordinator.Run(runCtx) }()

	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatalf("app task %d did not start", i)
		}
	}
	select {
	case id := <-started:
		t.Fatalf("third app task %s started while both slots were occupied", id)
	case <-time.After(40 * time.Millisecond):
	}
	release <- struct{}{}
	release <- struct{}{}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("third app task did not start after a slot was released")
	}
	release <- struct{}{}

	eventually(t, time.Second, func() bool {
		for _, task := range tasks {
			row, err := store.AppTaskByID(context.Background(), account.ID, app.ID, task.ID)
			if err != nil || row.Status != state.AppTaskSucceeded {
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
		t.Fatalf("maximum concurrent app tasks = %d, want 2", maximum.Load())
	}
}

func TestAppTaskCoordinatorRunIsDisabledByDefault(t *testing.T) {
	store, _, _, _, _ := newAppTaskCoordinatorFixture(t, 1, 10, 2048)
	counting := &claimCountingAppTaskStore{AppTaskStore: store}
	backend := appTaskBackendFunc(func(context.Context, AppTaskRestoreRequest) (AppTaskSession, error) {
		return nil, errors.New("must not be called")
	})
	coordinator := NewAppTaskCoordinator(counting, backend, AppTaskCoordinatorConfig{}, nil)
	if err := coordinator.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if counting.claims.Load() != 0 {
		t.Fatalf("claims = %d, want 0", counting.claims.Load())
	}
}

func TestNormalizeAppTaskOutcomeFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		outcome AppTaskOutcome
	}{
		{name: "untrusted status", outcome: AppTaskOutcome{Status: state.AppTaskCancelled}},
		{name: "success without exit", outcome: AppTaskOutcome{Status: state.AppTaskSucceeded}},
		{name: "failure without detail", outcome: AppTaskOutcome{Status: state.AppTaskFailed}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := normalizeAppTaskOutcome(test.outcome, 1024)
			if got.Status != state.AppTaskFailed || got.FailureCode != "guest_protocol_error" {
				t.Fatalf("normalizeAppTaskOutcome = %#v", got)
			}
		})
	}
}
