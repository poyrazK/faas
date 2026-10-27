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
	for _, tc := range []fallbackCase{
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
		for _, entry := range wakeEntryPoints {
			t.Run(tc.name+"/"+entry.name, func(t *testing.T) {
				f, ops := frozenWithTicket(t)
				tc.change(t, f)
				result, err := entry.wake(t, f)
				tc.check(t, f, ops, result, err)
			})
		}
	}
}

// wakeEntryPoints are the two ways a wake reaches phase 1: Wake directly,
// and the gateway's coordinated EnsureWake, which takes its selection from
// the thaw ticket (ADR-1740 amendment 2026-10-22).
var wakeEntryPoints = []struct {
	name string
	wake func(t *testing.T, f *sched.FrozenFixture) (sched.WakeResult, error)
}{
	{name: "wake", wake: func(t *testing.T, f *sched.FrozenFixture) (sched.WakeResult, error) {
		return (*f.E()).Wake(t.Context(), (*f.App()).ID, "", "", "")
	}},
	{name: "coordinated", wake: func(t *testing.T, f *sched.FrozenFixture) (sched.WakeResult, error) {
		out, err := (*f.E()).EnsureWake(t.Context(), (*f.App()).ID, sched.TriggerGateway)
		if err != nil || out.Instance == nil {
			return sched.WakeResult{}, err
		}
		return sched.WakeResult{InstanceID: out.Instance.InstanceID}, nil
	}},
}

type fallbackCase struct {
	name string
	// change runs after the freeze, before the wake.
	change func(t *testing.T, f *sched.FrozenFixture)
	// wantThawed: the ordinary path still thawed the same VM.
	wantThawed bool
	// wantErr: the ordinary path refuses the wake with this error.
	wantErr      error
	wantRefusals float64
}

func (tc fallbackCase) check(t *testing.T, f *sched.FrozenFixture, ops *wire.OpsMetrics, result sched.WakeResult, err error) {
	t.Helper()
	if tc.wantErr != nil {
		if !errors.Is(err, tc.wantErr) {
			t.Fatalf("wake = %v, want %v", err, tc.wantErr)
		}
		// The refused VM is frozen again, not lost.
		if row, _ := (*f.Store()).InstanceByID(t.Context(), (*f.Ins()).ID); row.State != string(state.StateFrozen) || !(*(*f.E()).XLedger()).IsFrozen((*f.Ins()).ID) {
			t.Fatalf("refused VM state %q, ledger frozen %v; want frozen", row.State, (*(*f.E()).XLedger()).IsFrozen((*f.Ins()).ID))
		}
	} else if err != nil {
		t.Fatalf("wake: %v", err)
	}
	got := thawOutcomes(ops)
	if got["fast"] != 0 || got["fast_refused"] != tc.wantRefusals {
		t.Fatalf("thaw outcomes = %v, want no fast thaw and %v refusals", got, tc.wantRefusals)
	}
	if thawedSame := err == nil && result.InstanceID == (*f.Ins()).ID; thawedSame != tc.wantThawed {
		t.Fatalf("ordinary path thawed the frozen VM = %v, want %v (result %+v)", thawedSame, tc.wantThawed, result)
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

func fastPathSkips(ops *wire.OpsMetrics) map[string]float64 {
	out := map[string]float64{}
	for _, reason := range wire.FrozenFastPathSkipReasons {
		if n := testutil.ToFloat64(ops.FrozenFastPathSkipped(reason)); n != 0 {
			out[reason] = n
		}
	}
	return out
}

// The gateway wakes through EnsureWake, whose coordinated selection kept
// every real wake off the fast path (production-us 2026-10-21: fast=0 and
// fast_refused=0 across all thaws while the unit tests, which call Wake,
// passed). A selection of the ticket's own deployment is the thaw the ticket
// allows.
func TestTicketedThawServesACoordinatedWake(t *testing.T) {
	f, ops := frozenWithTicket(t)
	counter := sched.CountReads(f)
	out, err := (*f.E()).EnsureWake(t.Context(), (*f.App()).ID, sched.TriggerGateway)
	if err != nil || out.Instance == nil || out.Instance.InstanceID != (*f.Ins()).ID || (*(*f.Vmm()).Thaws()) != 1 {
		t.Fatalf("EnsureWake: %+v, %v, thaws %d", out.Instance, err, (*(*f.Vmm()).Thaws()))
	}
	if got := thawOutcomes(ops); got["fast"] != 1 || got["success"] != 0 {
		t.Fatalf("thaw outcomes = %v, want one fast thaw", got)
	}
	if got := fastPathSkips(ops); len(got) != 0 {
		t.Fatalf("fast path skips = %v, want none", got)
	}
	// The ticket carries the workload-retirement gate's verdict.
	if reads := counter.Reads(); reads["ComputeNodeByID"] != 0 || reads["ManagedPostgresAdmissionFenced"] != 0 {
		t.Fatalf("coordinated ticketed thaw read %v after the selection; want none of the thaw path's reads", reads)
	}
	if n := testutil.ToFloat64(ops.FrozenThawTickets("issued")); n != 1 {
		t.Fatalf("tickets issued = %v, want 1", n)
	}
}

// A coordinated wake that selected another deployment, or found no ticket,
// takes the ordinary path, and the skip says why.
func TestFastPathSkipsAreCounted(t *testing.T) {
	t.Run("no ticket", func(t *testing.T) {
		f := sched.NewFrozenFixture(t, true)
		ops := wire.NewOpsMetrics("schedd")
		(*f.E()).WithOpsMetrics(ops)
		(*f.E()).EnableFrozenTier()
		if handled, err := (*f.E()).FreezeIdle(t.Context(), (*f.Ins()).ID, 150); err != nil || !handled {
			t.Fatalf("FreezeIdle: %v, %v", handled, err)
		}
		// Switched on after the freeze, as after a schedd restart.
		(*f.E()).EnableFrozenFastPath()
		if _, err := (*f.E()).EnsureWake(t.Context(), (*f.App()).ID, sched.TriggerGateway); err != nil {
			t.Fatalf("EnsureWake: %v", err)
		}
		if got := fastPathSkips(ops); got["no_ticket"] != 1 || len(got) != 1 {
			t.Fatalf("fast path skips = %v, want one no_ticket", got)
		}
		if got := thawOutcomes(ops); got["success"] != 1 {
			t.Fatalf("thaw outcomes = %v, want the ordinary thaw", got)
		}
	})
	// A deploy after the freeze: EnsureWake takes its selection from the
	// ticket, and the guarded thaw refuses it because the deploy moved the
	// app's runtime generation. The ordinary path then wakes the new
	// deployment; the old VM is frozen again, never published.
	t.Run("deployed after the freeze", func(t *testing.T) {
		f, ops := frozenWithTicket(t)
		next, err := (*f.Store()).CreateDeployment(t.Context(), state.Deployment{AppID: (*f.App()).ID, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:next", Status: state.DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		if err := (*f.Store()).MarkDeploymentLive(t.Context(), next.ID); err != nil {
			t.Fatal(err)
		}
		// The ordinary path may cold boot the new deployment; only the
		// fast path's decision is under test.
		_, _ = (*f.E()).EnsureWake(t.Context(), (*f.App()).ID, sched.TriggerGateway)
		if got := thawOutcomes(ops); got["fast"] != 0 || got["fast_refused"] != 1 {
			t.Fatalf("thaw outcomes = %v, want the guarded thaw refused once", got)
		}
		if row, _ := (*f.Store()).InstanceByID(t.Context(), (*f.Ins()).ID); row.State == string(state.StateRunning) {
			t.Fatal("the superseded deployment's VM was published RUNNING")
		}
	})
}
