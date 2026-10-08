package sched

// adr: 781
import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAppTaskRuntimeRejectsLostOriginalLeaseBeforeReservation(t *testing.T) {
	for _, change := range []string{"missing", "replacement", "queued", "expired", "cancelled"} {
		t.Run(change, func(t *testing.T) {
			s, account, app, dep, tasks := newAppTaskCoordinatorFixture(t, 1, 30, 2048)
			claimed, err := s.ClaimNextAppTask(t.Context(), "lease-test", time.Now(), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			request := AppTaskRestoreRequest{ID: tasks[0].ID, LeaseToken: *claimed.LeaseToken, AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Kind: tasks[0].Kind, DeploymentScope: dep.Scope, ArtifactKey: dep.RootfsKey, ImageDigest: dep.ImageDigest}
			switch change {
			case "missing":
				request.LeaseToken = ""
			case "replacement":
				request.LeaseToken = uuid.NewString()
			case "queued", "expired":
				if _, err := s.SweepExpiredAppTasks(t.Context(), time.Now().Add(2*time.Minute)); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				if _, err := s.RequestAppTaskCancellation(t.Context(), account.ID, app.ID, tasks[0].ID, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			e := newEngine(t, s, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
			if _, err := e.ResolveAppTaskRuntime(t.Context(), request); !errors.Is(err, state.ErrAppTaskLeaseLost) {
				t.Fatal("lost claim reached preparation", err)
			}
			if e.ledger.ResidentRAM() != 0 {
				t.Fatal("lost claim reserved capacity")
			}
		})
	}
}

type failedOriginalTaskRestore struct {
	*recordingRoutedAppTaskVMM
	err   error
	wrong bool
}

func (r *failedOriginalTaskRestore) RestoreAppTask(ctx context.Context, node string, spec AppTaskRestoreSpec) (*AppTaskRestoreOutcome, error) {
	outcome, _ := r.recordingRoutedAppTaskVMM.RestoreAppTask(ctx, node, spec)
	if r.wrong {
		outcome.Instance = uuid.NewString()
	}
	return outcome, r.err
}

func TestRegionalAppTaskCleanupKeepsOriginalOwnerOnUncertainty(t *testing.T) {
	for _, scenario := range []string{"success", "restore-error", "wrong-instance", "missing-owner"} {
		t.Run(scenario, func(t *testing.T) {
			e, _, source, app, request := regionalDispatchResourceFixture(t)
			task := uuid.NewString()
			instance, _ := runtimequota.AppTaskInstanceID(task, source.Permit.RunID)
			request.Instance, request.Kind = instance, KindAppTask
			if err := e.ledger.Admit(request); err != nil {
				t.Fatal(err)
			}
			issued, err := e.issueRegionalRuntimeLaunchSource(t.Context(), source.Permit.NodeID, instance, source.Permit.RunID, runtimequota.CapacityAppTask, task, app, source.Permit.Grant)
			if err != nil {
				t.Fatal(err)
			}
			original := *e.ledger.entries[instance].regionalSource
			var retired, released int
			unavailable := errors.New("original native stop unconfirmed")
			resolved := ResolvedAppTaskRuntime{NodeID: source.Permit.NodeID, Spec: AppTaskRestoreSpec{Instance: instance, TaskID: task, RunID: source.Permit.RunID, DeploymentID: app.DeploymentID, App: issued}, release: func() { released++ }}
			resolved.retire = func(ctx context.Context) error {
				if ctx.Err() != nil {
					t.Fatal("cleanup borrowed cancelled restore context")
				}
				retired++
				if retired == 1 {
					return unavailable
				}
				e.ledger.releaseOriginalRegionalRuntime(original) // Simulated accepted stop, never generic release.
				return nil
			}
			router := &failedOriginalTaskRestore{recordingRoutedAppTaskVMM: &recordingRoutedAppTaskVMM{}}
			switch scenario {
			case "restore-error":
				router.err = errors.New("lost restore reply")
			case "wrong-instance":
				router.wrong = true
			case "missing-owner":
				resolved.retire = nil
			}
			backend := NewRoutedVmmdAppTaskBackend(router, func(context.Context, AppTaskRestoreRequest) (ResolvedAppTaskRuntime, error) { return resolved, nil })
			session, err := backend.Restore(t.Context(), AppTaskRestoreRequest{ID: task, LeaseToken: source.Permit.RunID, DeploymentID: app.DeploymentID})
			if scenario == "success" {
				if err != nil || session == nil {
					t.Fatal(err)
				}
				if err := session.Destroy(t.Context()); !errors.Is(err, unavailable) {
					t.Fatal("uncertain stop released capacity", err)
				}
			} else if scenario == "missing-owner" {
				if !errors.Is(err, runtimequota.ErrTrust) || retired != 0 || router.restoreNode != "" {
					t.Fatal("missing owner reached transport", err)
				}
			} else if !errors.Is(err, unavailable) {
				t.Fatal("uncertainty discarded", err)
			}
			if e.ledger.ResidentRAM() != 200 || router.destroyNode != "" || released != 0 {
				t.Fatal("generic cleanup released original capacity")
			}
			if scenario == "missing-owner" {
				return
			}
			if session != nil {
				err = session.Destroy(t.Context())
			} else {
				err = destroyRestoredAppTask(t.Context(), router, resolved)
			}
			if err != nil || retired != 2 || e.ledger.ResidentRAM() != 0 || released != 0 || router.destroyNode != "" {
				t.Fatal("confirmed retry did not retire exactly original", err)
			}
		})
	}
}
