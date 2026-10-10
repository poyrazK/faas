package sched

// adr: 958 — interactive app tasks record their node before dispatch, carry
// the attach rendezvous to vmmd, and never fall back to batch execution.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type nodeAppTaskSession struct {
	appTaskSessionFuncs
	node string
}

func (s nodeAppTaskSession) NodeID() string { return s.node }

func createInteractiveAppTask(t *testing.T, store *state.MemStore, account state.Account, app state.App, deployment state.Deployment, tty bool) (state.AppTask, []byte) {
	t.Helper()
	digest := sha256.Sum256([]byte("attach-token"))
	task, err := store.CreateAppTask(context.Background(), state.CreateAppTaskParams{
		AccountID: account.ID, AppID: app.ID, DeploymentID: deployment.ID, Kind: state.AppTaskKindManual,
		Command: []string{"/bin/sh"}, TimeoutSeconds: 30,
		Interactive: &state.AppTaskInteractive{TTY: tty, TokenSHA256: digest[:]},
	})
	if err != nil {
		t.Fatalf("CreateAppTask(interactive): %v", err)
	}
	return task, digest[:]
}

func TestAppTaskCoordinatorInteractiveRecordsNodeBeforeDispatch(t *testing.T) {
	store, account, app, deployment, _ := newAppTaskCoordinatorFixture(t, 0, 10, 2048)
	task, digest := createInteractiveAppTask(t, store, account, app, deployment, true)
	destroyed := &atomic.Bool{}
	backend := appTaskBackendFunc(func(context.Context, AppTaskRestoreRequest) (AppTaskSession, error) {
		return nodeAppTaskSession{node: "node-7", appTaskSessionFuncs: appTaskSessionFuncs{
			execute: func(ctx context.Context, request AppTaskExecuteRequest) (AppTaskOutcome, error) {
				if request.Interactive == nil || !request.Interactive.TTY || !bytes.Equal(request.Interactive.TokenSHA256, digest) ||
					request.Interactive.AttachTimeoutSeconds != int(DefaultAppTaskAttachTimeout/time.Second) {
					return AppTaskOutcome{}, errors.New("interactive rendezvous missing from execute request")
				}
				target, err := store.AppTaskAttachTarget(ctx, account.ID, app.ID, task.ID)
				if err != nil || target.NodeID != "node-7" || target.TaskStatus != state.AppTaskRunning {
					return AppTaskOutcome{}, errors.New("node was not recorded before dispatch")
				}
				exit := 0
				return AppTaskOutcome{Status: state.AppTaskSucceeded, ExitCode: &exit}, nil
			},
			destroy: func(context.Context) error {
				destroyed.Store(true)
				return nil
			},
		}}, nil
	})
	coordinator := NewAppTaskCoordinator(store, backend, appTaskCoordinatorTestConfig(), nil)
	processed, err := coordinator.ProcessNext(context.Background())
	if err != nil || !processed {
		t.Fatalf("ProcessNext = %v, %v", processed, err)
	}
	row, err := store.AppTaskByID(context.Background(), account.ID, app.ID, task.ID)
	if err != nil || row.Status != state.AppTaskSucceeded || !destroyed.Load() {
		t.Fatalf("task = %#v, destroyed=%v, err=%v", row, destroyed.Load(), err)
	}
}

func TestAppTaskCoordinatorBatchTaskCarriesNoRendezvous(t *testing.T) {
	store, account, app, _, tasks := newAppTaskCoordinatorFixture(t, 1, 10, 2048)
	backend := appTaskBackendFunc(func(context.Context, AppTaskRestoreRequest) (AppTaskSession, error) {
		return appTaskSessionFuncs{
			execute: func(_ context.Context, request AppTaskExecuteRequest) (AppTaskOutcome, error) {
				if request.Interactive != nil {
					return AppTaskOutcome{}, errors.New("batch task received an attach rendezvous")
				}
				exit := 0
				return AppTaskOutcome{Status: state.AppTaskSucceeded, ExitCode: &exit}, nil
			},
			destroy: func(context.Context) error { return nil },
		}, nil
	})
	coordinator := NewAppTaskCoordinator(store, backend, appTaskCoordinatorTestConfig(), nil)
	if _, err := coordinator.ProcessNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	row, _ := store.AppTaskByID(context.Background(), account.ID, app.ID, tasks[0].ID)
	if row.Status != state.AppTaskSucceeded {
		t.Fatalf("status = %s", row.Status)
	}
}

func TestAppTaskCoordinatorInteractiveWithoutNodeFailsBeforeDispatch(t *testing.T) {
	store, account, app, deployment, _ := newAppTaskCoordinatorFixture(t, 0, 10, 2048)
	task, _ := createInteractiveAppTask(t, store, account, app, deployment, false)
	executed := &atomic.Bool{}
	destroyed := &atomic.Bool{}
	backend := appTaskBackendFunc(func(context.Context, AppTaskRestoreRequest) (AppTaskSession, error) {
		return appTaskSessionFuncs{
			execute: func(context.Context, AppTaskExecuteRequest) (AppTaskOutcome, error) {
				executed.Store(true)
				return AppTaskOutcome{}, nil
			},
			destroy: func(context.Context) error {
				destroyed.Store(true)
				return nil
			},
		}, nil
	})
	coordinator := NewAppTaskCoordinator(store, backend, appTaskCoordinatorTestConfig(), nil)
	if _, err := coordinator.ProcessNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	row, _ := store.AppTaskByID(context.Background(), account.ID, app.ID, task.ID)
	if executed.Load() || !destroyed.Load() || row.Status != state.AppTaskFailed || row.FailureCode == nil || *row.FailureCode != "attach_unroutable" {
		t.Fatalf("task = %#v executed=%v destroyed=%v", row, executed.Load(), destroyed.Load())
	}
}

type failingAttachStore struct {
	*state.MemStore
}

func (failingAttachStore) AppTaskAttachByTask(context.Context, string) (state.AppTaskAttach, error) {
	return state.AppTaskAttach{}, errors.New("database unavailable")
}

func TestAppTaskCoordinatorAttachLookupFailureNeverRunsAsBatch(t *testing.T) {
	store, account, app, deployment, _ := newAppTaskCoordinatorFixture(t, 0, 10, 2048)
	task, _ := createInteractiveAppTask(t, store, account, app, deployment, true)
	restored := &atomic.Bool{}
	backend := appTaskBackendFunc(func(context.Context, AppTaskRestoreRequest) (AppTaskSession, error) {
		restored.Store(true)
		return nil, errors.New("must not restore")
	})
	coordinator := NewAppTaskCoordinator(failingAttachStore{MemStore: store}, backend, appTaskCoordinatorTestConfig(), nil)
	processed, err := coordinator.ProcessNext(context.Background())
	if !processed || err == nil || restored.Load() {
		t.Fatalf("ProcessNext = %v, %v, restored=%v", processed, err, restored.Load())
	}
	row, _ := store.AppTaskByID(context.Background(), account.ID, app.ID, task.ID)
	if row.Status != state.AppTaskRestoring {
		t.Fatalf("status = %s, want restoring (replayable)", row.Status)
	}
}
