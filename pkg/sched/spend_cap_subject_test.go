// adr: 20261030093512 — an organization billing subject's spend cap governs its apps.
package sched_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// meterd bills an organization that owns its resources for its apps, so the
// wake gate reads that organization's cap, not the cap of the member who
// created the app.
func TestSpendCapSubjectIsTheBillingOrganization(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	member, err := store.CreateAccount(ctx, "cap-member-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	newOrg := func(name string) state.Org {
		t.Helper()
		org, err := store.CreateOrg(ctx, state.Org{Slug: name + "-" + uuid.NewString()[:8], Name: name, Plan: api.PlanFree, Status: state.OrgStatusActive})
		if err != nil {
			t.Fatal(err)
		}
		return org
	}
	// MemStore creates a team organization owning its resources: its own
	// billing subject.
	billed := newOrg("billed")
	if err := store.UpdateAccountPlan(ctx, billed.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	engine, err := sched.NewEngine(ctx, store, sched.NewNodeLedger(), nil, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		app  state.App
		want string
	}{
		{"personal app", state.App{AccountID: member.ID}, member.ID},
		{"billed organization", state.App{AccountID: member.ID, OrgID: billed.ID}, billed.ID},
		{"unreadable organization falls back to the account", state.App{AccountID: member.ID, OrgID: uuid.NewString()}, member.ID},
	} {
		if got := engine.XSpendCapSubject(ctx, tc.app); got != tc.want {
			t.Errorf("%s: subject = %s, want %s", tc.name, got, tc.want)
		}
	}
}
