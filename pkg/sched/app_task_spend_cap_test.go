// Cost-control review 2026-11-19 (N16): an app task VM is billed like an
// instance, so the spend cap holds it like a wake.
package sched_test

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

func TestAppTaskRuntimeRefusedAtTheSpendCap(t *testing.T) {
	store, account, app, deployment, tasks := sched.NewAppTaskCoordinatorFixture(t, 1, 30, 2048)
	var reached atomic.Bool
	reached.Store(true)
	vmm := &sched.FakeVMM{}
	e := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0").WithOverageChecker(switchableCap(&reached))
	request := sched.AppTaskRestoreRequest{ID: tasks[0].ID, AccountID: account.ID, AppID: app.ID, DeploymentID: deployment.ID}

	_, err := e.ResolveAppTaskRuntime(t.Context(), request)
	if !errors.Is(err, sched.ErrAppTaskSpendCap) {
		t.Fatalf("ResolveAppTaskRuntime at the cap = %v, want ErrAppTaskSpendCap", err)
	}
	var problem *api.Problem
	if !errors.As(err, &problem) || problem.Code != api.CodeAdmissionRefused {
		t.Fatalf("problem = %+v, want %s", problem, api.CodeAdmissionRefused)
	}
	if stage := sched.AppTaskFailureStage(err); stage != "admission" {
		t.Fatalf("stage = %q, want admission", stage)
	}
	if e.Ledger().ResidentRAM() != 0 {
		t.Fatal("a capped task reserved node RAM")
	}

	reached.Store(false)
	if _, err := e.ResolveAppTaskRuntime(t.Context(), request); errors.Is(err, sched.ErrAppTaskSpendCap) {
		t.Fatalf("ResolveAppTaskRuntime under the cap still refused: %v", err)
	}
}
