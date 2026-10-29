package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A company app keeps its creator's account_id, and so do its customers. A
// billing run on the creator's account bills them, through the creator's
// own billing connection, only while the creator is a member of the
// company: once they leave, the company's customers and their usage stop
// reaching the creator's runs (ADR-20261026151103 amendment 2).
//
// adr: 20261026151103
func TestMemBillingRunsBillCompanyCustomersOnlyForMembers(t *testing.T) {
	for _, member := range []bool{true, false} {
		billingRunCompanyCustomersContract(t, state.NewMemStore(), member)
	}
}

func billingRunCompanyCustomersContract(t *testing.T, store state.Store, member bool) {
	t.Helper()
	ctx := t.Context()
	runs, ok := store.(state.BillingRunStore)
	usage, okUsage := store.(state.ConsumerUsageStore)
	if !ok || !okUsage {
		t.Fatal("store has no billing runs or consumer usage")
	}
	suffix := uuid.NewString()[:8]
	creator, err := store.CreateAccount(ctx, "creator-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := store.CreateAccount(ctx, "owner-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "billing-co-" + suffix, Name: "Billing Co", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	for id, role := range map[string]state.OrgRole{owner.ID: state.OrgRoleOwner, creator.ID: state.OrgRoleAdmin} {
		if err := store.AddOrgMember(ctx, org.ID, id, role, nil); err != nil {
			t.Fatal(err)
		}
	}
	newApp := func(slug, orgID string) state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: orgID, Slug: slug + "-" + suffix, Type: state.AppTypeApp,
			RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	personal, company := newApp("personal", ""), newApp("company", org.ID)
	sept := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	customers := map[string]string{}
	for name, app := range map[string]state.App{"personal": personal, "company": company} {
		consumer, err := store.CreateAPIConsumer(ctx, creator.ID, app.ID, "cus_"+name+suffix, name)
		if err != nil {
			t.Fatal(err)
		}
		customers[consumer.ID] = name
		if _, err := usage.RecordAPIConsumerUsage(ctx, state.APIConsumerUsageEvent{EventID: uuid.NewString(), AccountID: creator.ID, AppID: app.ID,
			ConsumerKey: consumer.ID, WindowStart: sept.Add(time.Hour), RequestCount: 2, BillableUnits: 2}); err != nil {
			t.Fatal(err)
		}
	}
	if !member {
		if err := store.RemoveOrgMember(ctx, org.ID, creator.ID); err != nil {
			t.Fatal(err)
		}
	}

	run, err := runs.CreateBillingRun(ctx, state.BillingRun{AccountID: creator.ID, PeriodStart: sept, PeriodEnd: sept.AddDate(0, 1, 0),
		Trigger: state.BillingRunTriggerAPI, BillingRunOptions: state.BillingRunOptions{Sources: []string{state.BillingRunSourceAppConsumers},
			Finalize: true, Export: state.BillingRunExportNone}})
	if err != nil {
		t.Fatal(err)
	}
	now, lease := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), uuid.NewString()
	if _, err := runs.ClaimBillingRun(ctx, lease, now, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := runs.DiscoverBillingRunItems(ctx, run.ID, lease, "tenant", now); err != nil {
		t.Fatal(err)
	}
	items, err := runs.ListBillingRunItems(ctx, creator.ID, run.ID, "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	billed := map[string]bool{}
	for _, item := range items {
		billed[customers[item.SourceID]] = true
	}
	if !billed["personal"] || billed["company"] != member {
		t.Fatalf("member=%v: billed %v; want the personal customer, and the company's only while a member", member, billed)
	}
}
