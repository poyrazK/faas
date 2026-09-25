package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type platformTenantSelfStatementTestStore interface {
	state.PlatformTenantStatementStore
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreateApp(context.Context, state.App) (state.App, error)
	CreateAPIConsumer(context.Context, string, string, string, string) (state.APIConsumer, error)
	CreatePlatformTenant(context.Context, string, string, string, int) (state.PlatformTenant, bool, error)
	LinkPlatformTenantConsumer(context.Context, string, string, string) (state.APIConsumer, error)
}

func TestMemListFinalizedPlatformTenantStatementsIsBounded(t *testing.T) {
	testListFinalizedPlatformTenantStatementsIsBounded(t, state.NewMemStore())
}

func TestPgListFinalizedPlatformTenantStatementsIsBounded(t *testing.T) {
	store, _, _ := pgStoreWithPool(t)
	testListFinalizedPlatformTenantStatementsIsBounded(t, store)
}

func testListFinalizedPlatformTenantStatementsIsBounded(t *testing.T, store platformTenantSelfStatementTestStore) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "tenant-statement-read-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "statement-read", "Statement read", 250)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "tenant-read-" + uuid.NewString()[:8],
		Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := store.CreateAPIConsumer(ctx, account.ID, app.ID, "statement-reader", "Statement reader")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkPlatformTenantConsumer(ctx, account.ID, tenant.ID, consumer.ID); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	makeStatement := func(period time.Time, finalize bool) state.PlatformTenantStatement {
		t.Helper()
		statement, created, err := store.CreatePlatformTenantStatement(ctx, state.PlatformTenantStatementInput{
			AccountID: account.ID, TenantID: tenant.ID, PeriodStart: period, PeriodEnd: period.Add(time.Hour),
			Revision: 1, Currency: "EUR", BillableUnits: 1, AmountMillicents: 10, AsOf: time.Now().UTC(),
			Lines: []state.PlatformTenantStatementLine{{AppID: app.ID, ConsumerID: consumer.ID,
				WindowStart: period, BillableUnits: 1, RateCardID: uuid.NewString(), Currency: "EUR",
				PriceMillicentsPerUnit: 10, AmountMillicents: 10}},
		})
		if err != nil || !created {
			t.Fatalf("create statement = %+v, created=%v, err=%v", statement, created, err)
		}
		if finalize {
			statement, _, err = store.FinalizePlatformTenantStatement(ctx, account.ID, tenant.ID, statement.ID)
			if err != nil {
				t.Fatal(err)
			}
		}
		return statement
	}
	makeStatement(start, true)
	makeStatement(start.Add(24*time.Hour), false)
	makeStatement(start.Add(48*time.Hour), true)
	rows, err := store.ListFinalizedPlatformTenantStatements(ctx, account.ID, tenant.ID,
		start, start.Add(72*time.Hour), 2, 0)
	if err != nil || len(rows) != 2 || rows[0].PeriodStart != start.Add(48*time.Hour) || rows[1].PeriodStart != start {
		t.Fatalf("first finalized page = %+v, %v", rows, err)
	}
	rows, err = store.ListFinalizedPlatformTenantStatements(ctx, account.ID, tenant.ID,
		start, start.Add(72*time.Hour), 2, 1)
	if err != nil || len(rows) != 1 || rows[0].PeriodStart != start {
		t.Fatalf("second finalized page = %+v, %v", rows, err)
	}
}
