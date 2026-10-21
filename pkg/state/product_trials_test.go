package state

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

// productTrialSeed sets a customer's trial.
type productTrialSeed func(app, tenant string, start, end time.Time)

func TestProductTrialsMemory(t *testing.T) {
	s := NewMemStore()
	productTrialsContract(t, s, s.SeedProductPlanHistoryForTest, s.SeedProductTrialForTest)
}

// adr: 20261021195901 — the first assignment to a plan offering a trial
// starts it, later assignments keep it, and a customer never gets a second.
// Trial starts are free and use no included starts; a confirmation from the
// trial goes stale when it ends; a month's fee excludes the trial's minutes.
func productTrialsContract(t *testing.T, s productBillingRunTestStore, seedPlan productPlanSeed, seedTrial productTrialSeed) {
	t.Helper()
	ctx := t.Context()
	account, err := s.CreateAccount(ctx, "product-trials-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, App{AccountID: account.ID, Slug: "product-trials", Manifest: AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:trials"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	def, err := s.PutOperationDefinition(ctx, OperationDefinition{AccountID: account.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope,
		Spec: api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"generating"},
			InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.ProductCatalogSpec{Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", DefinitionID: def.ID}}, Plans: map[string]api.ProductPlan{
		"free": {Label: "Free", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 100, UnitPriceMillicents: 20000}}},
		"pro": {Label: "Pro", MonthlyFeeMillicents: 2900000, TrialDays: 14,
			Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 100, UnitPriceMillicents: 15000, IncludedStarts: 2}}}}}
	catalog, err := s.PutProductCatalog(ctx, account.ID, app.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	tenant := func(name string) string {
		value, _, err := s.CreatePlatformTenant(ctx, account.ID, name, name, 100)
		if err != nil {
			t.Fatal(err)
		}
		return value.ID
	}
	assign := func(tenant, plan string) api.ProductCustomer {
		t.Helper()
		customer, err := s.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: tenant, Plan: plan})
		if err != nil {
			t.Fatal(err)
		}
		return customer
	}

	// The first assignment to pro starts its 14-day trial; changing plans
	// keeps it.
	before := time.Now().UTC().Add(-time.Second)
	trialing := tenant("trialing")
	joined := assign(trialing, "pro")
	if joined.TrialStartsAt == nil || joined.TrialEndsAt == nil || joined.TrialStartsAt.Before(before) || !joined.TrialEndsAt.Equal(joined.TrialStartsAt.AddDate(0, 0, 14)) {
		t.Fatalf("first pro assignment = %+v", joined)
	}
	for _, plan := range []string{"free", "pro"} {
		if kept := assign(trialing, plan); kept.TrialStartsAt == nil || !kept.TrialStartsAt.Equal(*joined.TrialStartsAt) || !kept.TrialEndsAt.Equal(*joined.TrialEndsAt) {
			t.Fatalf("assignment to %s changed the trial: %+v", plan, kept)
		}
	}
	// A customer who joined on free starts the trial when moved to pro.
	upgrading := tenant("upgrading")
	if free := assign(upgrading, "free"); free.TrialStartsAt != nil {
		t.Fatalf("free assignment started a trial: %+v", free)
	}
	pro := assign(upgrading, "pro")
	if pro.TrialStartsAt == nil {
		t.Fatalf("first pro assignment of a free customer started no trial: %+v", pro)
	}
	// The owner moves a running trial's end within the next minute and 90
	// days of its start.
	trials := s.(ProductTrialStore)
	extended := pro.TrialStartsAt.AddDate(0, 0, 30)
	if moved, err := trials.SetProductCustomerTrialEnd(ctx, account.ID, app.ID, upgrading, extended); err != nil || moved.TrialEndsAt == nil || !moved.TrialEndsAt.Equal(extended) {
		t.Fatalf("extended trial = %+v %v", moved, err)
	}
	for _, bad := range []time.Time{time.Now().Add(-time.Minute), pro.TrialStartsAt.AddDate(0, 0, api.ProductControlMaxTrialDays+1)} {
		if _, err := trials.SetProductCustomerTrialEnd(ctx, account.ID, app.ID, upgrading, bad); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("trial end %s: %v", bad, err)
		}
	}
	if _, err := trials.SetProductCustomerTrialEnd(ctx, account.ID, app.ID, tenant("never"), time.Now().AddDate(0, 0, 1)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("trial of an unassigned tenant: %v", err)
	}
	if got, err := s.GetProductCustomer(ctx, account.ID, app.ID, upgrading); err != nil || got.TrialEndsAt == nil || !got.TrialEndsAt.Equal(extended) {
		t.Fatalf("stored trial = %+v %v", got, err)
	}

	// Starts during the trial are free and use no included starts.
	period := productcontrol.Period(time.Now())
	admit := func(key string, customer api.ProductCustomer, paid int64) (Operation, error) {
		op, _, err := s.AdmitOperation(ctx, OperationAdmission{AccountID: account.ID, PlatformTenantID: customer.TenantID, DefinitionID: def.ID, ProductFeature: "export",
			ProductControlEnabled: true, RequireProductQuote: true, Input: json.RawMessage(`{}`), IdempotencyKey: key,
			ExpectedProductQuote: productcontrol.Quote(account.ID, app.ID, customer.TenantID, "export", catalog, customer, period, paid, time.Now())})
		return op, err
	}
	duringQuote := productcontrol.Quote(account.ID, app.ID, trialing, "export", catalog, joined, period, 0, time.Now())
	for i, key := range []string{"trial-1", "trial-2", "trial-3"} {
		op, err := admit(key, joined, 0)
		if err != nil || op.ProductControl.UnitPriceMillicents != 0 || !op.ProductControl.Admission.Trial || op.ProductControl.Admission.StartsBefore != int64(i) {
			t.Fatalf("trial start %d: %+v %v", i+1, op.ProductControl, err)
		}
	}
	usage, err := s.GetProductUsage(ctx, account.ID, app.ID, trialing, period)
	if err != nil || usage.TrialStarts["export"] != 3 {
		t.Fatalf("usage trial starts = %+v %v", usage.TrialStarts, err)
	}

	// The trial ends now: the three trial starts leave pro's two included
	// starts unused, then starts cost the unit price, and a confirmation
	// from the trial goes stale once the price it bound changes.
	ended := time.Now().UTC()
	seedTrial(app.ID, trialing, *joined.TrialStartsAt, ended)
	after := joined
	after.TrialEndsAt = &ended
	for i, want := range []int64{0, 0, 15000} {
		op, err := admit("after-"+string(rune('a'+i)), after, int64(i))
		if err != nil || op.ProductControl.UnitPriceMillicents != want || op.ProductControl.Admission.Trial ||
			op.ProductControl.Admission.PaidStartsBefore == nil || *op.ProductControl.Admission.PaidStartsBefore != int64(i) {
			t.Fatalf("start %d after the trial: %+v %v", i+1, op.ProductControl, err)
		}
	}
	if op, _, err := s.AdmitOperation(ctx, OperationAdmission{AccountID: account.ID, PlatformTenantID: trialing, DefinitionID: def.ID, ProductFeature: "export",
		ProductControlEnabled: true, RequireProductQuote: true, Input: json.RawMessage(`{}`), IdempotencyKey: "stale", ExpectedProductQuote: duringQuote}); !errors.Is(err, ErrProductQuoteChanged) {
		t.Fatalf("confirmation from the trial once starts cost the unit price: %+v %v", op.ProductControl, err)
	}
	// A customer whose trial ended starts no other.
	if again := assign(trialing, "free"); again.TrialEndsAt == nil || !again.TrialEndsAt.Equal(ended) {
		t.Fatalf("trial after reassignment = %+v", again)
	}
	if again := assign(trialing, "pro"); again.TrialEndsAt == nil || !again.TrialEndsAt.Equal(ended) {
		t.Fatalf("a second trial started: %+v", again)
	}
	if _, err := trials.SetProductCustomerTrialEnd(ctx, account.ID, app.ID, trialing, time.Now().AddDate(0, 0, 1)); !errors.Is(err, ErrProductTrialUnavailable) {
		t.Fatalf("extending an ended trial: %v", err)
	}

	// A closed month: joined pro on the 6th with a trial to the 20th owes
	// pro's fee for the minutes from the 20th to the month's end.
	now := time.Now().UTC()
	lastStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	lastEnd := lastStart.AddDate(0, 1, 0)
	closed := tenant("closed")
	assign(closed, "free")
	trialStart, trialEnd := lastStart.AddDate(0, 0, 5), lastStart.AddDate(0, 0, 19)
	seedPlan(account.ID, app.ID, closed, catalog.Revision, "pro", true, trialStart)
	seedTrial(app.ID, closed, trialStart, trialEnd)
	review, err := s.ReviewProductBilling(ctx, ProductBillingScope{AccountID: account.ID, AppID: app.ID, TenantID: closed, Period: lastStart.Format("2006-01")})
	if err != nil {
		t.Fatal(err)
	}
	total, billable := int64(lastEnd.Sub(lastStart)/time.Minute), int64(lastEnd.Sub(trialEnd)/time.Minute)
	if len(review.Fees) != 1 || review.Fees[0].Plan != "pro" || review.Fees[0].AmountMillicents != (2900000*billable+total/2)/total ||
		review.Fees[0].TrialEndsAt == nil || !review.Fees[0].TrialEndsAt.Equal(trialEnd) {
		t.Fatalf("closed month with a trial = %+v", review.Fees)
	}
}
