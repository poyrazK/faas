//go:build !no_pg

package state_test

import (
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// An organization that owns its resources owns every cron of its apps,
// whoever created them: the cron cap ranks them under the organization and
// no longer against each creator's personal plan (bug hunt 2026-10-28 B37).
func TestPgEligibleCronsRankTheBillingSubject(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	suffix := uuid.NewString()[:8]
	creator, err := store.CreateAccount(ctx, "cron-creator-"+suffix+"@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	member, err := store.CreateAccount(ctx, "cron-member-"+suffix+"@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "cron-co-" + suffix, Name: "Cron Co", Plan: api.PlanFree})
	if err != nil {
		t.Fatal(err)
	}
	// Billing adoption is set by the provider confirmation path.
	if _, err := pool.Exec(ctx, `update orgs set resource_ownership_enabled = true where id = $1`, org.ID); err != nil {
		t.Fatal(err)
	}
	for id, role := range map[string]state.OrgRole{creator.ID: state.OrgRoleOwner, member.ID: state.OrgRoleDeveloper} {
		if err := store.AddOrgMember(ctx, org.ID, id, role, nil); err != nil {
			t.Fatal(err)
		}
	}
	cron := func(accountID, orgID, slug string) string {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: accountID, OrgID: orgID, Slug: slug + "-" + suffix, Type: state.AppTypeApp,
			RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		c, err := store.CreateCron(ctx, app.ID, "* * * * *", "/tick", true)
		if err != nil {
			t.Fatal(err)
		}
		return c.ID
	}
	personal := cron(creator.ID, "", "personal")
	company := cron(creator.ID, org.ID, "company")
	members := cron(member.ID, org.ID, "members")

	orgs, err := store.AccountEligibleCronIDs(ctx, org.ID, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(orgs) != 2 || !slices.Contains(orgs, company) || !slices.Contains(orgs, members) {
		t.Fatalf("organization eligible = %v, want both members' company crons", orgs)
	}
	creators, err := store.AccountEligibleCronIDs(ctx, creator.ID, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(creators) != 1 || creators[0] != personal {
		t.Fatalf("creator eligible = %v, want only the personal cron", creators)
	}
}
