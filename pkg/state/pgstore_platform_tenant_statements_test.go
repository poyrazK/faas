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
