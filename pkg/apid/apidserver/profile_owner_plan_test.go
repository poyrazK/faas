// adr: 819, 1155
package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Profiling judges an organization's app by the organization's plan, whoever
// asks or created it; a personal app keeps its account's plan.
func TestProfilePlanIsTheAppOwners(t *testing.T) {
	ctx := t.Context()
	mem := state.NewMemStore()
	member, err := mem.CreateAccount(ctx, "profile-plan-member@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	// A Free organization owns its resources at once; upgrading keeps them.
	org, err := mem.CreateOrg(ctx, state.Org{Slug: "profile-plan-team", Name: "Team", Plan: api.PlanFree, Status: state.OrgStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.UpdateOrgPlan(ctx, org.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	orgApp, err := mem.CreateApp(ctx, state.App{AccountID: member.ID, OrgID: org.ID, Slug: "profile-plan-org", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	personal, err := mem.CreateApp(ctx, state.App{AccountID: member.ID, Slug: "profile-plan-personal", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServer(mem, nil, "gregale.app", nil)
	if got := srv.ProfilePlanForTest(ctx, member.Plan, orgApp); got != api.PlanPro {
		t.Fatalf("organization app's profiling plan = %s, want the organization's pro", got)
	}
	if got := srv.ProfilePlanForTest(ctx, member.Plan, personal); got != api.PlanFree {
		t.Fatalf("personal app's profiling plan = %s, want the account's free", got)
	}
}
