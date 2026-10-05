// adr: 467 — no deployment-attached command crosses the cutover barrier.
package sched

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestManagedPostgresFenceBlocksTaskRuntimeAndPrime(t *testing.T) {
	for _, readErr := range []error{nil, errors.New("catalog unavailable")} {
		t.Run(cutoverAdmissionErrorName(readErr), func(t *testing.T) {
			ms, account, app, deployment, tasks := newAppTaskCoordinatorFixture(t, 1, 30, 2048)
			store := &cutoverFencedStore{MemStore: ms, readErr: readErr}
			vmm := &fakeVMM{}
			engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
			want := state.ErrManagedPostgresAdmissionFenced
			if readErr != nil {
				want = readErr
			}
			_, err := engine.ResolveAppTaskRuntime(t.Context(), AppTaskRestoreRequest{
				ID: tasks[0].ID, AccountID: account.ID, AppID: app.ID, DeploymentID: deployment.ID,
			})
			if !errors.Is(err, want) {
				t.Fatalf("task resolver: %v", err)
			}
			if err := engine.Prime(t.Context(), app.ID, deployment.ID); !errors.Is(err, want) {
				t.Fatalf("prime: %v", err)
			}
			if vmm.coldBoots != 0 || vmm.restores != 0 || engine.Ledger().ResidentRAM() != 0 {
				t.Fatal("fenced work acquired VM capacity")
			}
		})
	}
}

type cutoverTaskDispatchStore struct {
	state.AppTaskStore
	destroyed atomic.Bool
	completed atomic.Bool
}

func (s *cutoverTaskDispatchStore) MarkAppTaskRunning(context.Context, string, string, time.Time) (state.AppTask, error) {
	return state.AppTask{}, state.ErrManagedPostgresAdmissionFenced
}

func (s *cutoverTaskDispatchStore) CompleteAppTask(ctx context.Context, params state.CompleteAppTaskParams) (state.AppTask, error) {
	if !s.destroyed.Load() {
		return state.AppTask{}, errors.New("completion preceded VM destruction")
	}
	s.completed.Store(true)
	return s.AppTaskStore.CompleteAppTask(ctx, params)
}

func TestManagedPostgresTaskFenceAfterRestoreDestroysBeforeCompletion(t *testing.T) {
	for _, destroyFails := range []bool{false, true} {
		t.Run(cutoverTaskDestroyCase(destroyFails), func(t *testing.T) {
			ms, account, app, _, tasks := newAppTaskCoordinatorFixture(t, 1, 30, 2048)
			store := &cutoverTaskDispatchStore{AppTaskStore: ms}
			var executed atomic.Bool
			backend := appTaskBackendFunc(func(context.Context, AppTaskRestoreRequest) (AppTaskSession, error) {
				return appTaskSessionFuncs{
					execute: func(context.Context, AppTaskExecuteRequest) (AppTaskOutcome, error) {
						executed.Store(true)
						return AppTaskOutcome{}, nil
					},
					destroy: func(context.Context) error {
						if destroyFails {
							return errors.New("vmmd unavailable")
						}
						store.destroyed.Store(true)
						return nil
					},
				}, nil
			})
			c := NewAppTaskCoordinator(store, backend, appTaskCoordinatorTestConfig(), nil)
			processed, err := c.ProcessNext(t.Context())
			if !processed || (err != nil) != destroyFails || executed.Load() || store.completed.Load() == destroyFails {
				t.Fatalf("processed=%v err=%v executed=%v completed=%v", processed, err, executed.Load(), store.completed.Load())
			}
			task, err := ms.AppTaskByID(t.Context(), account.ID, app.ID, tasks[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if destroyFails {
				if task.Status != state.AppTaskRestoring || task.LeaseToken == nil {
					t.Fatal("unconfirmed destruction released task ownership")
				}
			} else if task.Status != state.AppTaskFailed || task.FailureCode == nil || *task.FailureCode != appTaskCutoverFailureCode {
				t.Fatalf("cutover failure not recorded: %+v", task)
			}
		})
	}
}

func cutoverTaskDestroyCase(fails bool) string {
	if fails {
		return "destroy_failure"
	}
	return "destroy_confirmed"
}
