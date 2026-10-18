// adr: 1740, 1741 — a ticketed thaw skips the ordinary wake path's reads, and any
// change it cannot vouch for sends the VM back to that path.
package sched

import (
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// frozenWithTicket freezes the fixture's instance with the fast path on,
// so the freeze records a thaw ticket.
func frozenWithTicket(t *testing.T) (*frozenFixture, *wire.OpsMetrics) {
	t.Helper()
	f := newFrozenFixture(t, true)
	ops := wire.NewOpsMetrics("schedd")
	f.e.WithOpsMetrics(ops)
	f.e.EnableFrozenTier()
	f.e.EnableFrozenFastPath()
	if handled, err := f.e.FreezeIdle(t.Context(), f.ins.ID, 150); err != nil || !handled {
		t.Fatalf("FreezeIdle: %v, %v", handled, err)
	}
	if _, ok := f.e.thawTickets.byApp[f.app.ID][f.ins.ID]; !ok {
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
	counter := countReads(f)
	result, err := f.e.Wake(t.Context(), f.app.ID, "", "", "")
	if err != nil || result.InstanceID != f.ins.ID || result.WakeID != f.ins.WakeID || f.vmm.thaws != 1 {
		t.Fatalf("Wake: %+v, %v, thaws %d", result, err, f.vmm.thaws)
	}
	if reads := counter.reads(); reads["AppByID"] != 0 || reads["ComputeNodeByID"] != 0 || reads["ManagedPostgresAdmissionFenced"] != 0 {
		t.Fatalf("ticketed thaw read %v; want none of the ordinary path's reads", reads)
	}
	if got := thawOutcomes(ops); got["fast"] != 1 || got["success"] != 0 {
		t.Fatalf("thaw outcomes = %v, want one fast thaw", got)
	}
	if result.Identity.TenantID != f.app.AccountID || result.Port != deploymentRuntimePort(f.dep) || result.DeploymentID != f.dep.ID {
		t.Fatalf("answer identity %+v port %d", result.Identity, result.Port)
	}
	if n := runningTransitions(t, f.store, f.ins.ID, "frozen_thaw"); n != 1 {
		t.Fatalf("frozen_thaw events = %d, want 1", n)
	}
	if _, ok := f.e.thawTickets.byApp[f.app.ID]; ok {
		t.Fatal("a used ticket stayed behind")
	}
}

func TestTicketedThawFallsBackToTheOrdinaryPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		// change runs after the freeze, before the wake.
		change func(t *testing.T, f *frozenFixture)
		// wantThawed: the ordinary path still thawed the same VM.
		wantThawed bool
		// wantErr: the ordinary path refuses the wake with this error.
		wantErr      error
		wantRefusals float64
	}{
		{name: "fast path switched off", change: func(_ *testing.T, f *frozenFixture) { f.e.frozenFastPath.Store(false) }, wantThawed: true},
		{name: "value written without a stamp", wantRefusals: 1, change: func(t *testing.T, f *frozenFixture) {
			if err := f.store.UpsertAppEnvInScope(t.Context(), f.app.AccountID, f.app.ID, "default", "EDITED", "1"); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "app resized", wantRefusals: 1, change: func(t *testing.T, f *frozenFixture) {
			ram := f.app.RAMMB * 2
			if _, err := f.store.UpdateApp(t.Context(), f.app.ID, state.UpdateAppParams{RAMMB: &ram}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "account suspended", wantRefusals: 1, wantErr: ErrPermanentWake, change: func(t *testing.T, f *frozenFixture) {
			if err := f.store.UpdateAccountStatus(t.Context(), f.app.AccountID, state.AccountSuspended); err != nil {
				t.Fatal(err)
			}
		}},
		// The ticket held the retirement gate's verdict; the ordinary path
		// gates before it decides anything.
		{name: "workload retired", wantRefusals: 1, wantErr: state.ErrEnvironmentWorkloadRetired, change: func(t *testing.T, f *frozenFixture) {
			retireWithoutNotification(t, f.e, f.store, f.app)
		}},
		// An operator cold boot (ADR-1523) after the freeze moves the
		// generation; the ordinary path then parks the VM it predates.
		{name: "operator cold boot", wantRefusals: 1, change: func(t *testing.T, f *frozenFixture) {
			if _, err := f.store.InvalidateSnapshotRecovery(t.Context(), f.dep.ID); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, ops := frozenWithTicket(t)
			tc.change(t, f)
			result, err := f.e.Wake(t.Context(), f.app.ID, "", "", "")
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Wake = %v, want %v", err, tc.wantErr)
				}
				// The refused VM is frozen again, not lost.
				if row, _ := f.store.InstanceByID(t.Context(), f.ins.ID); row.State != string(state.StateFrozen) || !f.e.ledger.IsFrozen(f.ins.ID) {
					t.Fatalf("refused VM state %q, ledger frozen %v; want frozen", row.State, f.e.ledger.IsFrozen(f.ins.ID))
				}
			} else if err != nil {
				t.Fatalf("Wake: %v", err)
			}
			got := thawOutcomes(ops)
			if got["fast"] != 0 || got["fast_refused"] != tc.wantRefusals {
				t.Fatalf("thaw outcomes = %v, want no fast thaw and %v refusals", got, tc.wantRefusals)
			}
			if thawedSame := err == nil && result.InstanceID == f.ins.ID; thawedSame != tc.wantThawed {
				t.Fatalf("ordinary path thawed the frozen VM = %v, want %v (result %+v)", thawedSame, tc.wantThawed, result)
			}
		})
	}
}

func TestTicketedThawLeavesARunningInstanceToPhaseOne(t *testing.T) {
	f, ops := frozenWithTicket(t)
	limits := api.MustLimitsFor(api.PlanPro)
	if err := f.e.Ledger().Admit(Request{Instance: "00000000-0000-4000-8000-00000000f001", AppID: f.app.ID, DeploymentID: f.dep.ID,
		Plan: api.PlanPro, RAMMB: f.app.RAMMB, VCPU: limits.VCPU, CPUMillicores: 1000, MaxConcurrency: f.app.MaxConcurrency, NodeID: f.ins.NodeID}); err != nil {
		t.Fatal(err)
	}
	if _, handled := f.e.thawFromTicketLocked(t.Context(), f.app.ID, "", "", f.ins.StartedAt); handled {
		t.Fatal("fast path thawed while the ledger holds a live instance of the app")
	}
	if got := thawOutcomes(ops); got["fast"] != 0 || f.vmm.thaws != 0 {
		t.Fatalf("thaw outcomes %v, thaws %d; want none", got, f.vmm.thaws)
	}
	if _, ok := f.e.thawTickets.byApp[f.app.ID][f.ins.ID]; !ok {
		t.Fatal("the skipped ticket was dropped")
	}
}

// An operator cold boot (ADR-1523) that lands after the freeze checked the
// instance's recovery lineage but before the ticket read the generation must
// not leave a ticket: the guarded thaw would compare a generation that
// already includes the recovery and thaw the VM it predates.
func TestThawTicketRefusesAVMThatPredatesAnOperatorColdBoot(t *testing.T) {
	f := newFrozenFixture(t, true)
	f.e.EnableFrozenTier()
	f.e.EnableFrozenFastPath()
	if _, err := f.e.thawTicketFor(t.Context(), f.ins, 150); err != nil {
		t.Fatalf("thawTicketFor before any recovery: %v", err)
	}
	if _, err := f.store.InvalidateSnapshotRecovery(t.Context(), f.dep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.e.thawTicketFor(t.Context(), f.ins, 150); !errors.Is(err, errNoThawTicket) {
		t.Fatalf("thawTicketFor after an operator cold boot = %v, want errNoThawTicket", err)
	}
}
