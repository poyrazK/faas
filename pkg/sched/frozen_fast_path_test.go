// adr: 1740, 1741 — a ticketed thaw skips the ordinary wake path's reads, and any
// change it cannot vouch for sends the VM back to that path.
package sched_test

import (
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// frozenWithTicket freezes the fixture's instance with the fast path on,
// so the freeze records a thaw ticket.
func frozenWithTicket(t *testing.T) (*sched.FrozenFixture, *wire.OpsMetrics) {
	t.Helper()
	f := sched.NewFrozenFixture(t, true)
	ops := wire.NewOpsMetrics("schedd")
	(*f.E()).WithOpsMetrics(ops)
	(*f.E()).EnableFrozenTier()
	(*f.E()).EnableFrozenFastPath()
	if handled, err := (*f.E()).FreezeIdle(t.Context(), (*f.Ins()).ID, 150); err != nil || !handled {
		t.Fatalf("FreezeIdle: %v, %v", handled, err)
	}
	if _, ok := (*(*(*f.E()).ThawTickets()).ByApp())[(*f.App()).ID][(*f.Ins()).ID]; !ok {
		t.Fatal("freezing with the fast path on recorded no thaw ticket")
	}
	return f, ops
}

func thawOutcomes(ops *wire.OpsMetrics) map[string]float64 {
	out := map[string]float64{}
	for _, outcome := range wire.FrozenThawOutcomes {
		out[outcome] = testutil.ToFloat64(ops.FrozenThawTotal(outcome))
	}
	return out
}

func TestTicketedThawReadsNothingBeforeThePublication(t *testing.T) {
	f, ops := frozenWithTicket(t)
	counter := sched.CountReads(f)
	result, err := (*f.E()).Wake(t.Context(), (*f.App()).ID, "", "", "")
	if err != nil || result.InstanceID != (*f.Ins()).ID || result.WakeID != (*f.Ins()).WakeID || (*(*f.Vmm()).Thaws()) != 1 {
		t.Fatalf("Wake: %+v, %v, thaws %d", result, err, (*(*f.Vmm()).Thaws()))
	}
	if reads := counter.Reads(); reads["AppByID"] != 0 || reads["ComputeNodeByID"] != 0 || reads["ManagedPostgresAdmissionFenced"] != 0 {
		t.Fatalf("ticketed thaw read %v; want none of the ordinary path's reads", reads)
	}
	if got := thawOutcomes(ops); got["fast"] != 1 || got["success"] != 0 {
		t.Fatalf("thaw outcomes = %v, want one fast thaw", got)
	}
	if result.Identity.TenantID != (*f.App()).AccountID || result.Port != sched.XDeploymentRuntimePort((*f.Dep())) || result.DeploymentID != (*f.Dep()).ID {
		t.Fatalf("answer identity %+v port %d", result.Identity, result.Port)
	}
	if n := sched.RunningTransitions(t, (*f.Store()), (*f.Ins()).ID, "frozen_thaw"); n != 1 {
		t.Fatalf("frozen_thaw events = %d, want 1", n)
	}
	if _, ok := (*(*(*f.E()).ThawTickets()).ByApp())[(*f.App()).ID]; ok {
		t.Fatal("a used ticket stayed behind")
	}
}

func TestTicketedThawFallsBackToTheOrdinaryPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		// change runs after the freeze, before the wake.
		change func(t *testing.T, f *sched.FrozenFixture)
		// wantThawed: the ordinary path still thawed the same VM.
		wantThawed bool
		// wantErr: the ordinary path refuses the wake with this error.
		wantErr      error
		wantRefusals float64
	}{
		{name: "fast path switched off", change: func(_ *testing.T, f *sched.FrozenFixture) { (*(*f.E()).FrozenFastPath()).Store(false) }, wantThawed: true},
		{name: "value written without a stamp", wantRefusals: 1, change: func(t *testing.T, f *sched.FrozenFixture) {
			if err := (*f.Store()).UpsertAppEnvInScope(t.Context(), (*f.App()).AccountID, (*f.App()).ID, "default", "EDITED", "1"); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "app resized", wantRefusals: 1, change: func(t *testing.T, f *sched.FrozenFixture) {
			ram := (*f.App()).RAMMB * 2
			if _, err := (*f.Store()).UpdateApp(t.Context(), (*f.App()).ID, state.UpdateAppParams{RAMMB: &ram}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "account suspended", wantRefusals: 1, wantErr: sched.ErrPermanentWake, change: func(t *testing.T, f *sched.FrozenFixture) {
			if err := (*f.Store()).UpdateAccountStatus(t.Context(), (*f.App()).AccountID, state.AccountSuspended); err != nil {
				t.Fatal(err)
			}
		}},
		// The ticket held the retirement gate's verdict; the ordinary path
		// gates before it decides anything.
		{name: "workload retired", wantRefusals: 1, wantErr: state.ErrEnvironmentWorkloadRetired, change: func(t *testing.T, f *sched.FrozenFixture) {
			retireWithoutNotification(t, (*f.E()), (*f.Store()), (*f.App()))
		}},
		// An operator cold boot (ADR-1523) after the freeze moves the
		// generation; the ordinary path then parks the VM it predates.
		{name: "operator cold boot", wantRefusals: 1, change: func(t *testing.T, f *sched.FrozenFixture) {
			if _, err := (*f.Store()).InvalidateSnapshotRecovery(t.Context(), (*f.Dep()).ID); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, ops := frozenWithTicket(t)
			tc.change(t, f)
			result, err := (*f.E()).Wake(t.Context(), (*f.App()).ID, "", "", "")
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Wake = %v, want %v", err, tc.wantErr)
				}
				// The refused VM is frozen again, not lost.
				if row, _ := (*f.Store()).InstanceByID(t.Context(), (*f.Ins()).ID); row.State != string(state.StateFrozen) || !(*(*f.E()).XLedger()).IsFrozen((*f.Ins()).ID) {
					t.Fatalf("refused VM state %q, ledger frozen %v; want frozen", row.State, (*(*f.E()).XLedger()).IsFrozen((*f.Ins()).ID))
				}
			} else if err != nil {
				t.Fatalf("Wake: %v", err)
			}
			got := thawOutcomes(ops)
			if got["fast"] != 0 || got["fast_refused"] != tc.wantRefusals {
				t.Fatalf("thaw outcomes = %v, want no fast thaw and %v refusals", got, tc.wantRefusals)
			}
			if thawedSame := err == nil && result.InstanceID == (*f.Ins()).ID; thawedSame != tc.wantThawed {
				t.Fatalf("ordinary path thawed the frozen VM = %v, want %v (result %+v)", thawedSame, tc.wantThawed, result)
			}
		})
	}
}

func TestTicketedThawLeavesARunningInstanceToPhaseOne(t *testing.T) {
	f, ops := frozenWithTicket(t)
	limits := api.MustLimitsFor(api.PlanPro)
	if err := (*f.E()).Ledger().Admit(sched.Request{Instance: "00000000-0000-4000-8000-00000000f001", AppID: (*f.App()).ID, DeploymentID: (*f.Dep()).ID,
		Plan: api.PlanPro, RAMMB: (*f.App()).RAMMB, VCPU: limits.VCPU, CPUMillicores: 1000, MaxConcurrency: (*f.App()).MaxConcurrency, NodeID: (*f.Ins()).NodeID}); err != nil {
		t.Fatal(err)
	}
	if _, handled := (*f.E()).ThawFromTicketLocked(t.Context(), (*f.App()).ID, "", "", (*f.Ins()).StartedAt); handled {
		t.Fatal("fast path thawed while the ledger holds a live instance of the app")
	}
	if got := thawOutcomes(ops); got["fast"] != 0 || (*(*f.Vmm()).Thaws()) != 0 {
		t.Fatalf("thaw outcomes %v, thaws %d; want none", got, (*(*f.Vmm()).Thaws()))
	}
	if _, ok := (*(*(*f.E()).ThawTickets()).ByApp())[(*f.App()).ID][(*f.Ins()).ID]; !ok {
		t.Fatal("the skipped ticket was dropped")
	}
}

// An operator cold boot (ADR-1523) that lands after the freeze checked the
// instance's recovery lineage but before the ticket read the generation must
// not leave a ticket: the guarded thaw would compare a generation that
// already includes the recovery and thaw the VM it predates.
func TestThawTicketRefusesAVMThatPredatesAnOperatorColdBoot(t *testing.T) {
	f := sched.NewFrozenFixture(t, true)
	(*f.E()).EnableFrozenTier()
	(*f.E()).EnableFrozenFastPath()
	if _, err := (*f.E()).ThawTicketFor(t.Context(), (*f.Ins()), 150); err != nil {
		t.Fatalf("thawTicketFor before any recovery: %v", err)
	}
	if _, err := (*f.Store()).InvalidateSnapshotRecovery(t.Context(), (*f.Dep()).ID); err != nil {
		t.Fatal(err)
	}
	if _, err := (*f.E()).ThawTicketFor(t.Context(), (*f.Ins()), 150); !errors.Is(err, (*sched.ErrNoThawTicket)) {
		t.Fatalf("thawTicketFor after an operator cold boot = %v, want errNoThawTicket", err)
	}
}
