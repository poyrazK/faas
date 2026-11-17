// adr: 20261104145353
//
// spend_cap_mirror_test.go pins that a mirror VM, which now bills, is
// refused at the account spend cap like any other capacity.
package sched_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAdmitMirrorInstance_SpendCapRefusesMirror(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := sched.SeedApp(t, store, api.PlanPro, 128, 5)
	vmm := &sched.FakeVMM{}
	var reached atomic.Bool
	reached.Store(true)
	e := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0").WithOverageChecker(switchableSpendCap(&reached))

	res, err := e.AdmitMirrorInstance(context.Background(), app.ID, "rule-1", dep.ID)
	if err == nil && res.InstanceID != "" {
		t.Fatalf("mirror admission at the spend cap = %+v, want no instance", res)
	}
	(*vmm.Mu()).Lock()
	boots := (*vmm.ColdBoots()) + (*vmm.Restores())
	(*vmm.Mu()).Unlock()
	if boots != 0 {
		t.Fatalf("boots=%d, want no mirror VM started past the cap", boots)
	}

	reached.Store(false)
	res, err = e.AdmitMirrorInstance(context.Background(), app.ID, "rule-1", dep.ID)
	if err != nil || res.InstanceID == "" {
		t.Fatalf("mirror admission under the cap = (%+v, %v), want a started instance", res, err)
	}
}
