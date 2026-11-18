// adr: 20261118070000

package sched_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// networkIdentityVMM restores every task VM at one host address and records
// whether the caller lookup still named the app when the VM was destroyed.
type networkIdentityVMM struct {
	hostIP           string
	callersAtDestroy func() int
	destroyed        int
	identityAtKill   int
}

func (v *networkIdentityVMM) RestoreAppTask(_ context.Context, _ string, spec sched.AppTaskRestoreSpec) (*sched.AppTaskRestoreOutcome, error) {
	return &sched.AppTaskRestoreOutcome{Instance: spec.Instance, HostIP: v.hostIP, Method: fcvm.WakeColdBoot}, nil
}

func (v *networkIdentityVMM) ExecuteAppTask(context.Context, string, string, apptaskproto.Request) (apptaskproto.Result, error) {
	return apptaskproto.Result{Status: apptaskproto.StatusSucceeded}, nil
}

func (v *networkIdentityVMM) Destroy(context.Context, string, string) error {
	v.destroyed++
	v.identityAtKill = v.callersAtDestroy()
	return nil
}

// An app task VM is the app on the service mesh while it runs, and stops
// being the app before vmmd frees its address.
func TestAppTaskVMActsAsTheAppOnTheServiceMesh(t *testing.T) {
	ctx := context.Background()
	store, account, app, deployment, _ := sched.NewAppTaskCoordinatorFixture(t, 1, 30, 2048)
	engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	task, err := store.ClaimNextAppTask(ctx, "network-identity-test", time.Now().UTC(), time.Minute)
	if err != nil || task.LeaseToken == nil {
		t.Fatalf("claim = %+v, %v", task, err)
	}
	const hostIP = "10.200.3.6"
	callers := func() int {
		got, err := store.AppTaskCallersByHostIP(ctx, "", hostIP)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range got {
			if c.AppID != app.ID || c.DeploymentID != deployment.ID {
				t.Fatalf("caller = %+v, want app %s deployment %s", c, app.ID, deployment.ID)
			}
		}
		return len(got)
	}
	vmm := &networkIdentityVMM{hostIP: hostIP, callersAtDestroy: callers}
	backend := sched.NewRoutedVmmdAppTaskBackend(vmm, engine.ResolveAppTaskRuntime)
	session, err := backend.Restore(ctx, sched.AppTaskRestoreRequest{
		ID: task.ID, AccountID: account.ID, AppID: app.ID, DeploymentID: deployment.ID,
		Kind: task.Kind, DeploymentScope: task.DeploymentScope, LeaseToken: *task.LeaseToken,
		ArtifactKey: task.ArtifactKey, ImageDigest: task.ImageDigest,
	})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if n := callers(); n != 0 {
		t.Fatalf("a restoring task is a caller (%d)", n)
	}
	if _, err := store.MarkAppTaskRunning(ctx, task.ID, *task.LeaseToken, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if n := callers(); n != 1 {
		t.Fatalf("running task VM resolves to %d callers, want the app", n)
	}
	if err := session.Destroy(ctx); err != nil {
		t.Fatal(err)
	}
	if vmm.destroyed != 1 || vmm.identityAtKill != 0 {
		t.Fatalf("destroyed %d times with %d callers still at its address, want once with none", vmm.destroyed, vmm.identityAtKill)
	}
}

var _ state.AppTaskNetworkStore = (*state.MemStore)(nil)
