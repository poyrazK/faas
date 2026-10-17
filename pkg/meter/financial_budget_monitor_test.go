package meter_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 566 — fresh partial usage can warn; transport failure and process restart
// must retain the same event and provider idempotency key.
func TestFinancialBudgetDeliveryRestart(t *testing.T) {
	store := state.NewMemStore()
	ctx := t.Context()
	a, err := store.CreateAccount(ctx, "budget-alert@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	price := state.FinancialPriceSnapshot{AccountID: a.ID, PeriodStart: start, PeriodEnd: start.AddDate(0, 1, 0), EffectiveFrom: start, Plan: api.PlanPro, DeliveryMode: "live", Price: financial.Price{Version: "budget-test-v1", Meter: "compute", Currency: "USD", Unit: "mb_seconds", UnitQuantity: 1, MillicentsPerUnit: 1}}
	if _, err := store.PutFinancialPriceSnapshot(ctx, price); err != nil {
		t.Fatal(err)
	}
	at := now.Add(-time.Minute).Truncate(time.Minute)
	if err := store.AppendUsage(ctx, a.ID, uuid.NewString(), uuid.NewString(), at, 90, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordFinancialSamplingWindow(ctx, at, true, false); err != nil {
		t.Fatal(err)
	}
	spec := financial.BudgetSpec{Name: "Monthly compute", Scope: financial.BudgetScope{Kind: "account"}, Currency: "USD", Meters: []string{"compute"}, Basis: "net_usage", LimitMillicents: 100, NotifyMillicents: []int64{80}, Mode: "monitored", Action: "notify", ResumeRule: "next_period", Enabled: true}
	p, err := store.CreateFinancialBudget(ctx, a.ID, uuid.NewString(), "editor", spec)
	if err != nil {
		t.Fatal(err)
	}
	now = time.Now().UTC()
	sender := &fakeMailer{sendErr: errors.New("provider failure with sensitive response")}
	loop := meter.NewLoop(store, nil, nil, nil, nil, sender, nil, nil, nil, func() time.Time { return now }, nil, nil, nil)
	if err := loop.RunFinancialBudgetsOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if sender.count() != 0 {
		t.Fatal("evaluation called the email provider")
	}
	if err := loop.RunFinancialBudgetDeliveriesOnce(ctx); err == nil {
		t.Fatal("transport failure hidden")
	}
	runtime, err := store.FinancialBudgetRuntime(ctx, a.ID, p.ID, p.Revision, start)
	if err != nil || len(runtime.Alerts) != 1 || runtime.Alerts[0].State != "retrying" || runtime.Alerts[0].LastError != "mail_delivery_failed" {
		t.Fatalf("failed delivery lost: %+v, %v", runtime, err)
	}
	if err := loop.RunFinancialBudgetsOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := loop.RunFinancialBudgetDeliveriesOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if sender.count() != 1 {
		t.Fatal("replay bypassed retry backoff")
	}

	// A new Loop has no in-process delivery memory.
	now = runtime.Alerts[0].NextAttemptAt
	sender.sendErr = nil
	restarted := meter.NewLoop(store, nil, nil, nil, nil, sender, nil, nil, nil, func() time.Time { return now }, nil, nil, nil)
	if err := restarted.RunFinancialBudgetDeliveriesOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if sender.count() != 2 || sender.sent[0].MessageID != sender.sent[1].MessageID || sender.sent[1].To[0] != a.Email || !strings.Contains(sender.sent[1].TextBody, "coverage is partial") {
		t.Fatalf("delivery contract: %+v", sender.sent)
	}
	if err := restarted.RunFinancialBudgetDeliveriesOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if sender.count() != 2 {
		t.Fatal("delivered event sent again")
	}
	if account, err := store.AccountByID(ctx, a.ID); err != nil || account.Status != state.AccountActive {
		t.Fatalf("notify mutated account status: %+v, %v", account, err)
	}
}
