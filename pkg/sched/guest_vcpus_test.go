// adr: 20261101160231

package sched_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGuestVCPUsFollowCPUQuotaOnlyForListedApps(t *testing.T) {
	engine, err := sched.NewEngine(context.Background(), state.NewMemStore(), sched.NewNodeLedger(), nil, nil, "", nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	listed := state.App{ID: "0b6f8c1e-2f6a-4c55-9d38-3c1f0c5e7a11", CPUMillicores: 500}
	other := state.App{ID: "7e0d4a52-91b3-4f7e-8a3c-5d2e9b1f6c44"}
	const planVCPUs = 2

	if got := engine.GuestVCPUs(listed, planVCPUs); got != planVCPUs {
		t.Fatalf("no list: listed app boots %d vCPUs, want the plan's %d", got, planVCPUs)
	}
	if err := engine.SetGuestVCPUFromCPUApps([]string{" " + strings.ToUpper(listed.ID) + " ", ""}); err != nil {
		t.Fatalf("SetGuestVCPUFromCPUApps: %v", err)
	}
	if got := engine.GuestVCPUs(listed, planVCPUs); got != 1 {
		t.Fatalf("listed app at 500 millicores boots %d vCPUs, want 1", got)
	}
	if got := engine.GuestVCPUs(other, planVCPUs); got != planVCPUs {
		t.Fatalf("unlisted app boots %d vCPUs, want the plan's %d", got, planVCPUs)
	}
	if err := engine.SetGuestVCPUFromCPUApps([]string{"*"}); err != nil {
		t.Fatalf("SetGuestVCPUFromCPUApps(*): %v", err)
	}
	if got := engine.GuestVCPUs(other, 4); got != 1 {
		t.Fatalf("* with the default 1,000 millicores boots %d vCPUs, want 1", got)
	}
	if err := engine.SetGuestVCPUFromCPUApps([]string{"not-an-app"}); err == nil {
		t.Fatal("SetGuestVCPUFromCPUApps accepted an entry that is neither an app ID nor *")
	}
	if err := engine.SetGuestVCPUFromCPUApps(nil); err != nil {
		t.Fatalf("SetGuestVCPUFromCPUApps(nil): %v", err)
	}
	if got := engine.GuestVCPUs(listed, planVCPUs); got != planVCPUs {
		t.Fatalf("cleared list: listed app boots %d vCPUs, want the plan's %d", got, planVCPUs)
	}
}
