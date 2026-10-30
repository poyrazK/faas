package state_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// schedd fires only the crons BillingSubjectEligibleCronIDs ranks within the
// plan's caps, so the ranking must cover the crons of an app's billing subject
// (ADR-202610171904): an adopted company's crons whoever created its apps, and
// never its creators' personal ones; before adoption the creator's personal
// and company crons together.
//
// adr: 202610171904
func TestMemEligibleCronsRankTheBillingSubject(t *testing.T) {
	for _, adopted := range []bool{true, false} {
		t.Run(fmt.Sprintf("adopted=%v", adopted), func(t *testing.T) {
			eligibleCronsSubjectContract(t, state.NewMemStore(), adopted, nil)
		})
	}
}

func eligibleCronsSubjectContract(t *testing.T, store state.Store, adopted bool, adopt func(orgID string)) {
	t.Helper()
	ctx, suffix := t.Context(), uuid.NewString()[:8]
	ranker, ok := store.(state.CronAccountQuotaStore)
	if !ok {
		t.Fatal("store cannot rank eligible crons")
	}
	account := func(name string) state.Account {
		t.Helper()
		acct, err := store.CreateAccount(ctx, name+"-"+suffix+"@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		return acct
	}
	creator, member := account("creator"), account("member")
	// A Free organization starts with billing adopted; a paid one awaits
	// adoption (ADR-202610171904).
	plan := api.PlanPro
	if adopted {
		plan = api.PlanFree
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "cron-co-" + suffix, Name: "Cron Co", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	if adopted && adopt != nil {
		adopt(org.ID)
	}
	for id, role := range map[string]state.OrgRole{creator.ID: state.OrgRoleOwner, member.ID: state.OrgRoleDeveloper} {
		if err := store.AddOrgMember(ctx, org.ID, id, role, nil); err != nil {
			t.Fatal(err)
		}
	}
	scale := api.MustLimitsFor(api.PlanScale)
	cron := func(accountID, orgID, slug string) string {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: accountID, OrgID: orgID, Slug: slug + "-" + suffix, Type: state.AppTypeApp,
			Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		c, err := store.CreateCronIfUnderQuota(ctx, app.ID, "*/5 * * * *", "/tick", true, scale)
		if err != nil {
			t.Fatal(err)
		}
		return c.ID
	}
	personal, company, members := cron(creator.ID, "", "personal"), cron(creator.ID, org.ID, "company"), cron(member.ID, org.ID, "members")

	want := map[string][]string{creator.ID: {personal, company}, member.ID: {members}, org.ID: nil}
	if adopted {
		want = map[string][]string{creator.ID: {personal}, member.ID: nil, org.ID: {company, members}}
	}
	for _, subject := range []struct{ name, id string }{{"creator", creator.ID}, {"member", member.ID}, {"organization", org.ID}} {
		got, err := ranker.BillingSubjectEligibleCronIDs(ctx, subject.id, 10, 10)
		if err != nil {
			t.Fatal(err)
		}
		slices.Sort(got)
		expected := slices.Sorted(slices.Values(want[subject.id]))
		if !slices.Equal(got, expected) {
			t.Fatalf("adopted=%v: %s's eligible crons = %v, want %v", adopted, subject.name, got, expected)
		}
	}
}
