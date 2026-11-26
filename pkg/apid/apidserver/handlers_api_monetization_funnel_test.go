package apidserver

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAPIMonetizationFunnelPricesRegistrationCohort(t *testing.T) {
	ctx := context.Background()
	e := setup(t, api.PlanHobby)
	appID := mustSeedApp(t, e, "funnel")
	otherApp := mustSeedApp(t, e, "funnel-empty")
	c, err := e.store.CreateAPIConsumer(ctx, e.acct.ID, appID, "pilot", "Pilot")
	if err != nil {
		t.Fatal(err)
	}
	minute := c.CreatedAt.UTC().Truncate(time.Minute)
	if _, err := e.store.CreateAPIConsumerRateCard(ctx, e.acct.ID, appID, "USD", 50_000, minute); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.RecordAPIConsumerUsage(ctx, state.APIConsumerUsageEvent{
		EventID: uuid.NewString(), AccountID: e.acct.ID, AppID: appID, ConsumerKey: c.ID, WindowStart: minute,
		RequestCount: 1, BillableUnits: 1,
	}); err != nil {
		t.Fatal(err)
	}
	funnel, err := e.s.apiMonetizationFunnel(ctx, e.store, e.acct.ID, appID, minute.AddDate(0, 0, 60))
	if err != nil || funnel.Registered != 1 || funnel.TotalRegistered != 1 || funnel.PricedInFirst30Days != 1 || funnel.LivePayment != 0 {
		t.Fatalf("cohort after sixty days = %+v, %v", funnel, err)
	}
	funnel, err = e.s.apiMonetizationFunnel(ctx, e.store, e.acct.ID, otherApp, minute.AddDate(0, 0, 60))
	if err != nil || funnel.Registered != 0 {
		t.Fatalf("other app cohort = %+v, %v", funnel, err)
	}
	resp := e.do(t, http.MethodGet, "/v1/apps/funnel/monetization-funnel", nil, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("funnel route: %d %s", resp.Code, resp.Body)
	}
	var result APIMonetizationFunnelResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil || result.Registered != 1 {
		t.Fatalf("funnel route body = %s, %v", resp.Body, err)
	}
}

func TestAPIMonetizationFunnelRepeatRequiresNextBillableMonth(t *testing.T) {
	january := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	february, march, april := january.AddDate(0, 1, 0), january.AddDate(0, 2, 0), january.AddDate(0, 3, 0)
	paidJanuary, paidFebruary, paidMarch := february.Add(time.Hour), march.Add(time.Hour), april.Add(time.Hour)
	sentJanuary := february
	first := state.APIConsumerMonetizationPeriod{Start: january, End: february, SentAt: &sentJanuary, PaidAt: &paidJanuary}
	adjustment := state.APIConsumerMonetizationPeriod{Start: january, End: february, PaidAt: &paidFebruary}
	crossing := state.APIConsumerMonetizationPeriod{Start: february, End: march, PaidAt: &paidFebruary}
	februaryDue := state.APIConsumerMonetizationPeriod{Start: february, End: march}
	marchPaid := state.APIConsumerMonetizationPeriod{Start: march, End: april, PaidAt: &paidMarch}
	overlappingFirst := first
	overlappingFirst.End = february.AddDate(0, 0, 2)

	for _, tt := range []struct {
		name     string
		periods  []state.APIConsumerMonetizationPeriod
		eligible int
		repeated int
	}{
		{"same month revision", []state.APIConsumerMonetizationPeriod{adjustment, first}, 0, 0},
		{"later overlapping statement", []state.APIConsumerMonetizationPeriod{crossing, overlappingFirst}, 0, 0},
		{"next billable month unpaid", []state.APIConsumerMonetizationPeriod{marchPaid, februaryDue, first}, 1, 0},
		{"next billable month paid", []state.APIConsumerMonetizationPeriod{marchPaid, crossing, first}, 1, 1},
		{"gap with no billable statement", []state.APIConsumerMonetizationPeriod{marchPaid, first}, 1, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out APIMonetizationFunnelResponse
			out.addInvoiceMilestones(state.APIConsumerMonetizationCustomer{RegisteredAt: january, Periods: tt.periods}, april.AddDate(0, 0, 2))
			if out.LiveInvoiceSent != 1 || out.LivePayment != 1 || out.NextBillableMonth != tt.eligible || out.NextMonthLivePayment != tt.repeated {
				t.Fatalf("invoice milestones = %+v", out)
			}
		})
	}
}

func TestAPISecondInvoiceReviewSelectsActionAndKeepsFunnelTotals(t *testing.T) {
	january := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	february, march, april := january.AddDate(0, 1, 0), january.AddDate(0, 2, 0), january.AddDate(0, 3, 0)
	firstPaid, secondPaid := february.Add(time.Hour), march.Add(time.Hour)
	first := state.APIConsumerMonetizationPeriod{Start: january, End: february, PaidAt: &firstPaid}
	hold := state.APIConsumerMonetizationPeriod{Start: february, End: march, Draft: true,
		StatementID: "held-statement", UsageHoldRun: "hold-run"}
	draft := state.APIConsumerMonetizationPeriod{Start: march, End: april, Draft: true, StatementID: "draft-statement"}
	open := state.APIConsumerMonetizationPeriod{Start: february, End: march, StatementID: "open-statement",
		InvoiceProvider: state.BillingProviderStripe, InvoiceLive: true, PaymentStatus: state.InvoicePaymentOpen}
	testInvoice := open
	testInvoice.StatementID, testInvoice.InvoiceLive, testInvoice.PaymentStatus = "test-statement", false, state.InvoicePaymentPaid
	paid := open
	paid.StatementID, paid.PaymentStatus, paid.PaidAt = "paid-statement", state.InvoicePaymentPaid, &secondPaid

	for _, tc := range []struct {
		name, status, statement, holdRun string
		periods                          []state.APIConsumerMonetizationPeriod
		billable, repeated               int
	}{
		{name: "held month before finalized", status: "usage_hold", statement: "held-statement", holdRun: "hold-run",
			periods: []state.APIConsumerMonetizationPeriod{draft, hold}},
		{name: "draft before finalized", status: "statement_draft", statement: "draft-statement",
			periods: []state.APIConsumerMonetizationPeriod{draft}},
		{name: "one row for finalized month", status: "awaiting_payment", statement: "open-statement",
			periods: []state.APIConsumerMonetizationPeriod{hold, testInvoice, open}, billable: 1},
		{name: "test payment cannot count", status: "test_invoice", statement: "test-statement",
			periods: []state.APIConsumerMonetizationPeriod{testInvoice}, billable: 1},
		{name: "confirmed repeat payment", status: "paid", statement: "paid-statement",
			periods: []state.APIConsumerMonetizationPeriod{open, paid}, billable: 1, repeated: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out APIMonetizationFunnelResponse
			out.addInvoiceMilestones(state.APIConsumerMonetizationCustomer{
				ID: "customer", Name: "Pilot customer", RegisteredAt: january,
				Periods: append([]state.APIConsumerMonetizationPeriod{first}, tc.periods...),
			}, april.Add(time.Hour))
			if out.NextBillableMonth != tc.billable || out.NextMonthLivePayment != tc.repeated ||
				len(out.SecondInvoiceReview) != 1 {
				t.Fatalf("unexpected second invoice result: %+v", out)
			}
			row := out.SecondInvoiceReview[0]
			if row.ConsumerID != "customer" || row.Status != tc.status || row.StatementID != tc.statement || row.HoldRunID != tc.holdRun {
				t.Fatalf("unexpected review row: %+v", row)
			}
		})
	}
}
