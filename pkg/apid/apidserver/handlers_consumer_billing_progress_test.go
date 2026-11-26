package apidserver

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAPIConsumerBillingProgressTracksPositiveLivePayment(t *testing.T) {
	ctx := context.Background()
	e := setup(t, api.PlanHobby)
	appID := mustSeedApp(t, e, "billing-progress")
	consumer, err := e.store.CreateAPIConsumer(ctx, e.acct.ID, appID, "cus_PROGRESS", "Progress")
	if err != nil {
		t.Fatal(err)
	}
	minute := consumer.CreatedAt.UTC().Truncate(time.Minute)
	progressAt := func(at time.Time) api.APIConsumerBillingProgressResponse {
		t.Helper()
		p, err := e.s.consumerBillingProgress(ctx, e.store, e.acct.ID, appID, consumer, at)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	if p := progressAt(minute.Add(time.Minute)); p.Usage.Status != "waiting" || p.FirstPricedAt != nil {
		t.Fatalf("before first request: %+v", p)
	}
	recordUsage := func(at time.Time, units int64) {
		t.Helper()
		if _, err := e.store.RecordAPIConsumerUsage(ctx, state.APIConsumerUsageEvent{
			EventID: uuid.NewString(), AccountID: e.acct.ID, AppID: appID, ConsumerKey: consumer.ID,
			WindowStart: at, RequestCount: 1, BillableUnits: units,
		}); err != nil {
			t.Fatal(err)
		}
	}
	recordUsage(minute, 0)
	if p := progressAt(minute.Add(time.Minute)); p.Usage.Status != "not_billable" || p.Usage.RequestCount != 1 {
		t.Fatalf("non-billable request: %+v", p)
	}
	recordUsage(minute, 1)
	if p := progressAt(minute.Add(time.Minute)); p.Usage.Status != "unpriced" || p.Usage.UnpricedUnits != 1 || p.FirstPricedAt != nil {
		t.Fatalf("unpriced usage: %+v", p)
	}
	if _, err := e.store.CreateAPIConsumerRateCard(ctx, e.acct.ID, appID, "USD", 50_000, minute); err != nil {
		t.Fatal(err)
	}
	if p := progressAt(minute.Add(time.Minute)); p.Usage.Status != "priced" || p.Usage.AmountMillicents != 50_000 ||
		p.FirstPricedAt == nil || !p.FirstPricedAt.Equal(minute) {
		t.Fatalf("first priced minute: %+v", p)
	}
	if p := progressAt(minute.AddDate(0, 0, 45)); p.Usage.Status != "waiting" || p.FirstPricedAt == nil || !p.FirstPricedAt.Equal(minute) {
		t.Fatalf("activation window moved with recent usage: %+v", p)
	}

	draft := func(at time.Time) state.APIConsumerUsageStatement {
		t.Helper()
		s, _, err := e.s.draftAPIConsumerStatement(ctx, e.acct, appID, consumer.ID, e.store, at, at.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	export := func(s state.APIConsumerUsageStatement, id string, live bool) state.StatementInvoiceExport {
		t.Helper()
		if _, _, err := e.store.FinalizeAPIConsumerUsageStatement(ctx, e.acct.ID, appID, consumer.ID, s.ID); err != nil {
			t.Fatal(err)
		}
		out, _, err := e.store.BeginStatementInvoiceExport(ctx, state.StatementInvoiceExport{
			AccountID: e.acct.ID, SourceKind: state.StatementSourceAppConsumer, StatementID: s.ID,
			Provider: state.BillingProviderStripe, Customer: consumer.ExternalRef, Currency: "USD", AmountMinor: 50, Livemode: live,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.store.SetStatementInvoiceExportInvoice(ctx, e.acct.ID, out.ID, id); err != nil {
			t.Fatal(err)
		}
		out, err = e.store.CompleteStatementInvoiceExport(ctx, e.acct.ID, out.ID)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	recordPayment := func(invoice state.StatementInvoiceExport, payment state.StatementInvoicePayment) {
		t.Helper()
		if _, err := e.store.RecordStatementInvoicePayment(ctx, invoice.ID, payment, minute.Add(time.Hour), nil); err != nil {
			t.Fatal(err)
		}
	}
	testStatement := draft(minute)
	now := minute.Add(3 * time.Minute)
	if p := progressAt(now); p.Statement == nil || p.Statement.ID != testStatement.ID || p.Statement.Status != "draft" || p.Invoice != nil {
		t.Fatalf("statement draft: %+v", p)
	}
	testInvoice := export(testStatement, "in_testProgress", false)
	paidTestAt := minute.Add(time.Minute)
	recordPayment(testInvoice, state.StatementInvoicePayment{Status: "paid", AmountPaidMinor: 50, PaidAt: &paidTestAt})
	if p := progressAt(now); p.Invoice == nil || p.Invoice.PaidAt == nil || p.Invoice.Livemode || p.FirstLivePaidAt != nil {
		t.Fatalf("test payment marked live: %+v", p)
	}
	recordUsage(minute.Add(time.Minute), 1)
	liveInvoice := export(draft(minute.Add(time.Minute)), "in_liveProgress", true)
	recordPayment(liveInvoice, state.StatementInvoicePayment{Status: "open", AmountRemainingMinor: 50})
	if p := progressAt(now); p.Invoice == nil || p.Invoice.ID != "in_liveProgress" || p.FirstLivePaidAt != nil {
		t.Fatalf("test payment displaced live invoice: %+v", p)
	}
	paidLiveAt := minute.Add(2 * time.Minute)
	recordPayment(liveInvoice, state.StatementInvoicePayment{Status: "paid", PaidAt: &paidLiveAt})
	if p := progressAt(now); p.FirstLivePaidAt != nil || p.Invoice == nil || p.Invoice.PaidAt != nil {
		t.Fatalf("zero payment marked complete: %+v", p)
	}
	recordPayment(liveInvoice, state.StatementInvoicePayment{Status: "paid", AmountPaidMinor: 50, PaidAt: &paidLiveAt})
	if p := progressAt(now); p.FirstLivePaidAt == nil || !p.FirstLivePaidAt.Equal(paidLiveAt) || p.Invoice.ID != "in_liveProgress" {
		t.Fatalf("live payment missing: %+v", p)
	}
	route := "/v1/apps/billing-progress/consumers/" + consumer.ID + "/billing-progress"
	if res := e.do(t, http.MethodGet, route, nil, nil); res.Code != http.StatusOK {
		t.Fatalf("billing progress route: %d %s", res.Code, res.Body)
	}
	mustSeedApp(t, e, "another-app")
	if res := e.do(t, http.MethodGet, "/v1/apps/another-app/consumers/"+consumer.ID+"/billing-progress", nil, nil); res.Code != http.StatusNotFound {
		t.Fatalf("unrelated app read: %d %s", res.Code, res.Body)
	}
}
