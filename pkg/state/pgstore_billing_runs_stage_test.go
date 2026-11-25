package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261104120000 — a stage's consumers are never billed: a billing run
// discovers neither their usage nor their monthly base fee.
func TestPgBillingRunSkipsStageConsumers(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	suffix := uuid.NewString()[:8]
	account, err := store.CreateAccount(ctx, "stage-billing-"+suffix+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "stage-billing-" + suffix})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "stage-billing-" + suffix,
		Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	sept := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if _, err := store.CreateAPIConsumerRateCardVersion(ctx, state.APIConsumerRateCardInput{AccountID: account.ID, AppID: app.ID,
		Currency: "EUR", PriceMillicentsPerUnit: 10, Unit: state.APIConsumerRateCardUnitRequest, EffectiveFrom: sept.AddDate(0, -1, 0),
		BaseFeeMillicentsPerMonth: 1000}); err != nil {
		t.Fatal(err)
	}
	production, err := store.CreateAPIConsumer(ctx, account.ID, app.ID, "production-customer", "Production")
	if err != nil {
		t.Fatal(err)
	}
	staged, err := store.CreateAPIConsumerInEnvironment(ctx, account.ID, app.ID, "staging", "stage-customer", "Stage")
	if err != nil {
		t.Fatal(err)
	}
	// Usage an N-1 gateway could still record for the stage consumer.
	for _, consumerID := range []string{production.ID, staged.ID} {
		if _, err := store.RecordAPIConsumerUsage(ctx, state.APIConsumerUsageEvent{EventID: uuid.NewString(), AccountID: account.ID,
			AppID: app.ID, ConsumerKey: consumerID, WindowStart: sept.Add(time.Hour), RequestCount: 2, BillableUnits: 2}); err != nil {
			t.Fatal(err)
		}
	}

	run, err := store.CreateBillingRun(ctx, state.BillingRun{AccountID: account.ID, PeriodStart: sept, PeriodEnd: sept.AddDate(0, 1, 0),
		BillingRunOptions: state.BillingRunOptions{Sources: []string{state.BillingRunSourceAppConsumers}}, Trigger: state.BillingRunTriggerAPI})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	if _, err := store.ClaimBillingRun(ctx, "owner", now, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.DiscoverBillingRunItems(ctx, run.ID, "owner", "", now); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListBillingRunItems(ctx, account.ID, run.ID, "", "", 10)
	if err != nil || len(items) != 1 || items[0].SourceID != production.ID {
		t.Fatalf("items = %+v, %v; want only the production consumer", items, err)
	}
}
