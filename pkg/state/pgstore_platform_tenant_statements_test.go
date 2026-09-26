package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgPlatformTenantStatementHandoffExcludesAppClaim(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	tenant, _, err := store.CreatePlatformTenant(ctx, accountID, "billing-tenant-"+uuid.NewString()[:8], "Billing tenant", 250)
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := store.CreateAPIConsumer(ctx, accountID, appID, "billing-consumer", "Billing consumer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkPlatformTenantConsumer(ctx, accountID, tenant.ID, consumer.ID); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	makeTenantStatement := func(minute time.Time, revision int) state.PlatformTenantStatement {
		t.Helper()
		var priorStatus state.APIConsumerUsageStatementStatus
		if revision > 1 {
			priorStatus = state.APIConsumerUsageStatementFinalized
		}
		statement, created, err := store.CreatePlatformTenantStatement(ctx, state.PlatformTenantStatementInput{
			AccountID: accountID, TenantID: tenant.ID, PeriodStart: minute, PeriodEnd: minute.Add(time.Hour),
			Revision: revision, PriorStatus: priorStatus, Currency: "EUR", BillableUnits: 2, AmountMillicents: 20, AsOf: time.Now().UTC(),
			Lines: []state.PlatformTenantStatementLine{{AppID: appID, ConsumerID: consumer.ID, WindowStart: minute,
				BillableUnits: 2, RateCardID: uuid.NewString(), Currency: "EUR", PriceMillicentsPerUnit: 10, AmountMillicents: 20}},
		})
		if err != nil || !created {
			t.Fatalf("create tenant statement: %+v, %v, %v", statement, created, err)
		}
		statement, changed, err := store.FinalizePlatformTenantStatement(ctx, accountID, tenant.ID, statement.ID)
		if err != nil || !changed {
			t.Fatalf("finalize tenant statement: %+v, %v, %v", statement, changed, err)
		}
		return statement
	}
	first := makeTenantStatement(start, 1)
	claim := state.PlatformTenantStatementHandoffInput{AccountID: accountID, TenantID: tenant.ID,
		StatementID: first.ID, ExternalInvoiceID: "tenant-pg-invoice-1"}
	if _, created, err := store.CreatePlatformTenantStatementHandoff(ctx, claim); err != nil || !created {
		t.Fatalf("tenant claim: %v, %v", created, err)
	}
	if _, created, err := store.CreatePlatformTenantStatementHandoff(ctx, claim); err != nil || created {
		t.Fatalf("tenant claim replay: %v, %v", created, err)
	}
	adjustment := makeTenantStatement(start, 2)
	if _, created, err := store.CreatePlatformTenantStatementHandoff(ctx, state.PlatformTenantStatementHandoffInput{
		AccountID: accountID, TenantID: tenant.ID, StatementID: adjustment.ID, ExternalInvoiceID: "tenant-pg-adjustment",
	}); err != nil || !created {
		t.Fatalf("same-period adjustment handoff: %v, %v", created, err)
	}
	appStatement, _, err := store.CreateAPIConsumerUsageStatement(ctx, state.APIConsumerUsageStatementInput{
		AccountID: accountID, AppID: appID, ConsumerID: consumer.ID, PeriodStart: start, PeriodEnd: start.Add(time.Hour),
		Currency: "EUR", BillableUnits: 2, AmountMillicents: 20, Priced: true, AsOf: time.Now().UTC(),
		Buckets: []state.APIConsumerUsageStatementBucket{{WindowStart: start, BillableUnits: 2, RateCardID: uuid.NewString(),
			Currency: "EUR", PriceMillicentsPerUnit: 10, AmountMillicents: 20}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.FinalizeAPIConsumerUsageStatement(ctx, accountID, appID, consumer.ID, appStatement.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateAPIConsumerUsageStatementHandoff(ctx, state.APIConsumerUsageStatementHandoffInput{
		AccountID: accountID, AppID: appID, ConsumerID: consumer.ID, StatementID: appStatement.ID,
		ExternalInvoiceID: "app-pg-overlap",
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("app overlap err=%v", err)
	}
	second := makeTenantStatement(start.Add(2*time.Hour), 1)
	if _, _, err := store.CreatePlatformTenantStatementHandoff(ctx, state.PlatformTenantStatementHandoffInput{
		AccountID: accountID, TenantID: tenant.ID, StatementID: second.ID, ExternalInvoiceID: claim.ExternalInvoiceID,
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("external invoice reuse err=%v", err)
	}
	if _, created, err := store.CreatePlatformTenantStatementHandoff(ctx, state.PlatformTenantStatementHandoffInput{
		AccountID: accountID, TenantID: tenant.ID, StatementID: second.ID, ExternalInvoiceID: "tenant-pg-invoice-2",
	}); err != nil || !created {
		t.Fatalf("non-overlapping tenant claim: %v, %v", created, err)
	}
	thirdStart := start.Add(4 * time.Hour)
	appFirst, _, err := store.CreateAPIConsumerUsageStatement(ctx, state.APIConsumerUsageStatementInput{
		AccountID: accountID, AppID: appID, ConsumerID: consumer.ID, PeriodStart: thirdStart, PeriodEnd: thirdStart.Add(time.Hour),
		Currency: "EUR", BillableUnits: 2, AmountMillicents: 20, Priced: true, AsOf: time.Now().UTC(),
		Buckets: []state.APIConsumerUsageStatementBucket{{WindowStart: thirdStart, BillableUnits: 2, RateCardID: uuid.NewString(),
			Currency: "EUR", PriceMillicentsPerUnit: 10, AmountMillicents: 20}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.FinalizeAPIConsumerUsageStatement(ctx, accountID, appID, consumer.ID, appFirst.ID); err != nil {
		t.Fatal(err)
	}
	if _, created, err := store.CreateAPIConsumerUsageStatementHandoff(ctx, state.APIConsumerUsageStatementHandoffInput{
		AccountID: accountID, AppID: appID, ConsumerID: consumer.ID, StatementID: appFirst.ID,
		ExternalInvoiceID: "app-pg-first",
	}); err != nil || !created {
		t.Fatalf("app claim before tenant: %v, %v", created, err)
	}
	third := makeTenantStatement(thirdStart, 1)
	if _, _, err := store.CreatePlatformTenantStatementHandoff(ctx, state.PlatformTenantStatementHandoffInput{
		AccountID: accountID, TenantID: tenant.ID, StatementID: third.ID, ExternalInvoiceID: "tenant-pg-overlap",
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("tenant overlap err=%v", err)
	}
}

// adr: 239
func TestPgTenantSurfaceUsageStatementAndHandoff(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	tenant, _, err := store.CreatePlatformTenant(ctx, accountID, "surface-billing-"+uuid.NewString()[:8], "Surface billing", 250)
	if err != nil {
		t.Fatal(err)
	}
	surfaceID := uuid.NewString()
	start := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	event := state.APIConsumerUsageEvent{EventID: uuid.NewString(), AccountID: accountID, AppID: appID,
		ConsumerKey: state.AnonymousConsumerKey, PlatformTenantID: tenant.ID,
		PlatformTenantSurfaceID: surfaceID, WindowStart: start, RequestCount: 3, BillableUnits: 3}
	if applied, err := store.RecordAPIConsumerUsage(ctx, event); err != nil || !applied {
		t.Fatalf("record surface event applied=%t err=%v", applied, err)
	}
	if applied, err := store.RecordAPIConsumerUsage(ctx, event); err != nil || applied {
		t.Fatalf("replay surface event applied=%t err=%v", applied, err)
	}
	minutes, err := store.ListPlatformTenantUsageMinutes(ctx, accountID, tenant.ID, start, start.Add(time.Minute))
	if err != nil || len(minutes) != 1 || minutes[0].SurfaceID != surfaceID || minutes[0].ConsumerKey != "" || minutes[0].BillableUnits != 3 {
		t.Fatalf("tenant surface minutes=%+v err=%v", minutes, err)
	}
	days, err := store.ListPlatformTenantUsage(ctx, accountID, tenant.ID, start, start.Add(time.Minute))
	if err != nil || len(days) != 1 || days[0].SurfaceID != surfaceID || days[0].BillableUnits != 3 {
		t.Fatalf("tenant surface days=%+v err=%v", days, err)
	}
	input := state.PlatformTenantStatementInput{AccountID: accountID, TenantID: tenant.ID,
		PeriodStart: start, PeriodEnd: start.Add(time.Hour), Revision: 1, Currency: "EUR",
		BillableUnits: 3, AmountMillicents: 30, AsOf: time.Now().UTC(),
		Lines: []state.PlatformTenantStatementLine{{AppID: appID, SurfaceID: surfaceID, WindowStart: start,
			BillableUnits: 3, RateCardID: uuid.NewString(), Currency: "EUR", PriceMillicentsPerUnit: 10, AmountMillicents: 30}}}
	statement, created, err := store.CreatePlatformTenantStatement(ctx, input)
	if err != nil || !created || len(statement.Lines) != 1 || statement.Lines[0].SurfaceID != surfaceID {
		t.Fatalf("surface statement=%+v created=%t err=%v", statement, created, err)
	}
	if _, _, err := store.FinalizePlatformTenantStatement(ctx, accountID, tenant.ID, statement.ID); err != nil {
		t.Fatal(err)
	}
	if _, created, err := store.CreatePlatformTenantStatementHandoff(ctx, state.PlatformTenantStatementHandoffInput{
		AccountID: accountID, TenantID: tenant.ID, StatementID: statement.ID, ExternalInvoiceID: "surface-invoice-1",
	}); err != nil || !created {
		t.Fatalf("surface handoff created=%t err=%v", created, err)
	}
	other, _, err := store.CreatePlatformTenant(ctx, accountID, "surface-next-"+uuid.NewString()[:8], "Next owner", 250)
	if err != nil {
		t.Fatal(err)
	}
	input.TenantID = other.ID
	second, _, err := store.CreatePlatformTenantStatement(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.FinalizePlatformTenantStatement(ctx, accountID, other.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreatePlatformTenantStatementHandoff(ctx, state.PlatformTenantStatementHandoffInput{
		AccountID: accountID, TenantID: other.ID, StatementID: second.ID, ExternalInvoiceID: "surface-invoice-2",
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("overlapping surface handoff err=%v", err)
	}
}
