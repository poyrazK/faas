// adr: 1740 — a ticketed thaw refuses everything the wake path refuses: the
// admission checks the ticket stands in for move the app's runtime
// generation, and the rest run before the VM resumes. A ticketed wake the
// fast path declines runs every check the fresh path runs.
package sched_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/onebox-faas/faas/pkg/wire"
)

// admissionFixture is a frozen function app in a personal organization, so
// the application-standards rule applies to it. With fastPath its VM froze
// with a thaw ticket; without, every wake takes the fresh path.
func admissionFixture(t *testing.T, fastPath bool) (*sched.FrozenFixture, *wire.OpsMetrics, state.Org) {
	t.Helper()
	mem := state.NewMemStore()
	var org state.Org
	f := sched.NewFrozenFixtureInStore(t, mem, true, func(app *state.App) {
		owner := app.AccountID
		created, err := mem.CreateOrg(t.Context(), state.Org{Slug: "personal-" + uuid.NewString()[:8], Name: "Personal", Personal: true,
			PersonalOwnerAccountID: &owner, Plan: api.PlanPro, Status: state.OrgStatusActive})
		if err != nil {
			t.Fatal(err)
		}
		if err := mem.AddOrgMember(t.Context(), created.ID, owner, state.OrgRoleOwner, nil); err != nil {
			t.Fatal(err)
		}
		org, app.OrgID = created, created.ID
	})
	ops := wire.NewOpsMetrics("schedd")
	(*f.E()).WithOpsMetrics(ops)
	(*f.E()).EnableFrozenTier()
	if fastPath {
		(*f.E()).EnableFrozenFastPath()
	}
	if handled, err := (*f.E()).FreezeIdle(t.Context(), (*f.Ins()).ID, 150); err != nil || !handled {
		t.Fatalf("FreezeIdle: %v, %v", handled, err)
	}
	if fastPath {
		if n := testutil.ToFloat64(ops.FrozenThawTickets("issued")); n != 1 {
			t.Fatalf("personal-organization app got no ticket (issued %v)", n)
		}
	}
	return f, ops, org
}

// refusingStore makes the admission checks a thaw ticket does not stand in
// for refuse, without moving the app's runtime generation: a held strict
// compute cap (budget rows move no generation) and a pending standards
// enrollment (the worst case: a refusal the guarded thaw would not see).
type refusingStore struct {
	MemStore         *state.MemStore
	appID            string
	budgetHeld       atomic.Bool
	standardsPending atomic.Bool
}

const heldBudgetID = "00000000-0000-4000-8000-0000000b0d9e"

func (s *refusingStore) FinancialComputePolicies(ctx context.Context, account, app string) ([]state.FinancialBudget, error) {
	if !s.budgetHeld.Load() || app != s.appID {
		return s.MemStore.FinancialComputePolicies(ctx, account, app)
	}
	return []state.FinancialBudget{{ID: heldBudgetID, AccountID: account, Spec: financial.BudgetSpec{Name: "App compute",
		Scope: financial.BudgetScope{Kind: "app", ID: app}, Currency: "USD", Meters: []string{"compute"}, Basis: "gross_usage",
		Mode: "strict", Action: "suspend_workloads", ResumeRule: "manual", NotifyMillicents: []int64{}, Enabled: true}}}, nil
}

func (s *refusingStore) FinancialComputeBalance(ctx context.Context, p state.FinancialBudget, now time.Time) (financial.ComputeBalance, error) {
	if p.ID == heldBudgetID {
		return financial.ComputeBalance{Initialized: true, AppID: s.appID, Held: true}, nil
	}
	return s.MemStore.FinancialComputeBalance(ctx, p, now)
}

func (s *refusingStore) GetApplicationStandardEnrollment(ctx context.Context, orgID, appID string) (state.ApplicationStandardEnrollment, error) {
	enrollment, err := s.MemStore.GetApplicationStandardEnrollment(ctx, orgID, appID)
	if err == nil && s.standardsPending.Load() && appID == s.appID {
		enrollment.State = "pending"
	}
	return enrollment, err
}

// refuse makes the fixture's engine read through a refusingStore.
func refuse(f *sched.FrozenFixture) *refusingStore {
	store := &refusingStore{MemStore: *f.Store(), appID: (*f.App()).ID}
	f.SetStore(statetest.NewMem(store))
	return store
}

func TestTicketedThawRefusesWhatTheWakePathRefuses(t *testing.T) {
	for _, tc := range []struct {
		name string
		// change runs after the freeze, before the wake.
		change func(t *testing.T, f *sched.FrozenFixture, org state.Org)
		want   error
		// generation: the change moves the app's runtime generation, so
		// the guarded thaw is what refuses a ticketed wake that gets that
		// far; otherwise a check before the resume refuses it.
		generation bool
	}{
		{name: "nothing changed"},
		{name: "account suspended", want: sched.ErrPermanentWake, generation: true, change: func(t *testing.T, f *sched.FrozenFixture, _ state.Org) {
			if err := (*f.Store()).UpdateAccountStatus(t.Context(), (*f.App()).AccountID, state.AccountSuspended); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "workload retired", want: state.ErrEnvironmentWorkloadRetired, generation: true, change: func(t *testing.T, f *sched.FrozenFixture, _ state.Org) {
			retireWithoutNotification(t, *f.E(), *f.Store(), *f.App())
		}},
		{name: "application standards pending", want: state.ErrApplicationStandardsPending, change: func(_ *testing.T, f *sched.FrozenFixture, _ state.Org) {
			refuse(f).standardsPending.Store(true)
		}},
		{name: "deployment quarantined", want: sched.ErrPermanentWake, generation: true, change: func(t *testing.T, f *sched.FrozenFixture, _ state.Org) {
			if err := (*f.Store()).SetDeploymentParked(t.Context(), (*f.Dep()).ID, string(state.ParkReasonSecurityScanRegressed), time.Now()); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "compute budget held", want: state.ErrFinancialBudgetHeld, change: func(_ *testing.T, f *sched.FrozenFixture, _ state.Org) {
			refuse(f).budgetHeld.Store(true)
		}},
	} {
		for _, entry := range wakeEntryPoints {
			t.Run(tc.name+"/"+entry.name, func(t *testing.T) {
				// The fresh path decides with the fast path off.
				fresh, _, freshOrg := admissionFixture(t, false)
				ticketed, ops, ticketedOrg := admissionFixture(t, true)
				before := generationOf(t, ticketed)
				if tc.change != nil {
					tc.change(t, fresh, freshOrg)
					tc.change(t, ticketed, ticketedOrg)
				}
				if moved := generationOf(t, ticketed) != before; moved != tc.generation {
					t.Fatalf("change moved the app's runtime generation = %v, want %v", moved, tc.generation)
				}
				_, freshErr := entry.wake(t, fresh)
				result, err := entry.wake(t, ticketed)
				if (tc.want == nil) != (freshErr == nil) || tc.want != nil && !errors.Is(freshErr, tc.want) {
					t.Fatalf("fresh path = %v, want %v", freshErr, tc.want)
				}
				if tc.want == nil {
					if err != nil || result.InstanceID != (*ticketed.Ins()).ID {
						t.Fatalf("ticketed wake = %+v, %v; want the frozen VM thawed", result, err)
					}
					if got := thawOutcomes(ops); got["fast"] != 1 {
						t.Fatalf("thaw outcomes = %v, want the fast path", got)
					}
					return
				}
				if !errors.Is(err, tc.want) {
					t.Fatalf("ticketed wake = %v, want %v as the fresh path refused", err, tc.want)
				}
				if row, _ := (*ticketed.Store()).InstanceByID(t.Context(), (*ticketed.Ins()).ID); row.State == string(state.StateRunning) {
					t.Fatal("the refused wake published the frozen VM RUNNING")
				}
				if got := thawOutcomes(ops); got["fast"] != 0 {
					t.Fatalf("thaw outcomes = %v, want no fast thaw", got)
				}
				if !tc.generation && *(*ticketed.Vmm()).Thaws() != 0 {
					t.Fatalf("vmmd thaws = %d; a refusal the generation does not cover must stop before the VM resumes", *(*ticketed.Vmm()).Thaws())
				}
			})
		}
	}
}

func generationOf(t *testing.T, f *sched.FrozenFixture) int64 {
	t.Helper()
	g, err := (*f.Store()).AppRuntimeGeneration(t.Context(), (*f.App()).ID)
	if err != nil {
		t.Fatal(err)
	}
	return g
}
