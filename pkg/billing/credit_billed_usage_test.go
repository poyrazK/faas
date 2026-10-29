package billing

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func invoiceLines(complete bool, categories ...string) *state.InvoiceDetails {
	items := []state.InvoiceLineItem{}
	for i, category := range categories {
		net := int64(1000)
		if category == "Usage" {
			net = 10
		}
		items = append(items, state.InvoiceLineItem{ID: "line-" + string(rune('a'+i)), ChargeCategory: category, NetCents: net})
	}
	return &state.InvoiceDetails{Lines: &state.InvoiceLines{Complete: complete, Items: items}}
}

// adr: 20261028101500 — an itemised invoice offsets the usage it billed, even
// when its service period holds no usage (usage is billed in arrears); an
// incomplete or uncategorised snapshot falls back to the period.
func TestComputeInvoiceOverageCentsUsesBilledUsageLines(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "lines@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	period := time.Date(2026, 10, 27, 20, 4, 41, 0, time.UTC)
	tests := []struct {
		name    string
		details *state.InvoiceDetails
		want    int64
	}{
		{"plan and usage lines", invoiceLines(true, "Purchase", "Usage"), 10},
		{"plan only", invoiceLines(true, "Purchase"), 0},
		{"incomplete snapshot falls back", invoiceLines(false, "Purchase", "Usage"), 0},
		{"uncategorised line falls back", invoiceLines(true, "", "Usage"), 0},
		{"no details falls back", nil, 0},
	}
	for _, tt := range tests {
		got, err := ComputeInvoiceOverageCents(ctx, store, state.Invoice{AccountID: acct.ID, Plan: api.PlanHobby,
			PeriodStart: period, PeriodEnd: period.AddDate(0, 1, 0), Details: tt.details})
		if err != nil || got != tt.want {
			t.Errorf("%s: overage = %d, %v; want %d", tt.name, got, err, tt.want)
		}
	}
}

// adr: 20261028101500 — the sandbox renewal: $10 plan + $0.10 usage, a $10.05
// discount, $0.05 paid and a period stored as start == end. 10 cents are
// billed usage, but only the 5 paid cents may be drained and refunded.
func TestConsumeCreditsForDiscountedRenewal(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "renewal@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAccountCredit(ctx, state.AccountCredit{AccountID: acct.ID, CentsRemaining: 100, Reason: "probe"}); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 27, 20, 19, 41, 0, time.UTC)
	if err := store.UpsertInvoice(ctx, state.Invoice{AccountID: acct.ID, Provider: "polar", ProviderInvoiceID: "order-renewal", Status: "paid",
		Currency: "usd", Plan: api.PlanHobby, PeriodStart: at, PeriodEnd: at, SubtotalCents: 1010, TotalCents: 5, AmountPaidCents: 5,
		Details: invoiceLines(true, "Purchase", "Usage")}); err != nil {
		t.Fatal(err)
	}
	inv, err := store.GetInvoiceByProviderID(ctx, acct.ID, "polar", "order-renewal")
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := ConsumeCreditsForInvoiceUpTo(ctx, store, inv.ID, inv.AmountPaidCents, "test", "billed usage")
	if err != nil || res.ConsumedCents != 5 || res.RemainingCreditsCents != 95 {
		t.Fatalf("consumed = %+v, %v; want 5 consumed, 95 left", res, err)
	}
}
