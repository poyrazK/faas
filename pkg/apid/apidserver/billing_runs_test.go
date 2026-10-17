package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

// billingRunFixture is an account with last month's usage from customers
// that end a billing run differently.
type billingRunFixture struct {
	e      apidserver.TestEnv
	stripe *fakeStripe
	month  time.Time
}

func newBillingRunFixture(t *testing.T) billingRunFixture {
	t.Helper()
	withBillingKeys(t)
	e := apidserver.Setup(t, api.PlanHobby)
	stripe := &fakeStripe{}
	srv := httptest.NewServer(stripe)
	t.Cleanup(srv.Close)
	(*e.S()).StatementInvoices().Stripe = &stripeinvoice.Client{BaseURL: srv.URL, HTTP: srv.Client()}
	now := time.Now().UTC()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	// Two days into the current month, last month has settled.
	(*e.S()).BillingRuns().Now = func() time.Time { return month.AddDate(0, 1, 2) }
	if res := e.Do(t, http.MethodPut, "/v1/account/billing-connections/stripe", api.PutBillingConnectionRequest{SecretKey: "rk_test_51abcdWXYZ"}, nil); res.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", res.Code, res.Body)
	}
	return billingRunFixture{e: e, stripe: stripe, month: month}
}

func (f billingRunFixture) consumer(t *testing.T, slug, externalRef string, units int64, tenantID string) api.APIConsumerResponse {
	t.Helper()
	created := f.e.Do(t, http.MethodPost, "/v1/apps/"+slug+"/consumers", api.CreateAPIConsumerRequest{ExternalRef: externalRef, Name: externalRef}, nil)
	var consumer api.APIConsumerResponse
	if err := json.Unmarshal(created.Body.Bytes(), &consumer); err != nil || created.Code != http.StatusCreated {
		t.Fatalf("create consumer: %d %s", created.Code, created.Body)
	}
	store := *f.e.Store()
	if tenantID != "" {
		if _, err := store.LinkPlatformTenantConsumer(context.Background(), (*f.e.Acct()).ID, tenantID, consumer.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.RecordAPIConsumerUsage(context.Background(), state.APIConsumerUsageEvent{EventID: uuid.NewString(),
		AccountID: (*f.e.Acct()).ID, AppID: consumer.AppID, ConsumerKey: consumer.ID, PlatformTenantID: tenantID,
		WindowStart: f.month.Add(time.Hour), RequestCount: units, BillableUnits: units}); err != nil {
		t.Fatal(err)
	}
	return consumer
}

// drain runs worker ticks until the run completes.
func (f billingRunFixture) drain(t *testing.T, runID string) api.BillingRunResponse {
	t.Helper()
	for range 20 {
		if err := (*f.e.S()).BillingRuns().RunOnce(context.Background()); err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		res := f.e.Do(t, http.MethodGet, "/v1/account/billing-runs/"+runID, nil, nil)
		var run api.BillingRunResponse
		if err := json.Unmarshal(res.Body.Bytes(), &run); err != nil || res.Code != http.StatusOK {
			t.Fatalf("get run: %d %s", res.Code, res.Body)
		}
		if run.Status == "completed" {
			return run
		}
	}
	t.Fatal("billing run did not complete")
	return api.BillingRunResponse{}
}

func (f billingRunFixture) start(t *testing.T, req api.CreateBillingRunRequest) api.BillingRunResponse {
	t.Helper()
	res := f.e.Do(t, http.MethodPost, "/v1/account/billing-runs", req, nil)
	var run api.BillingRunResponse
	if err := json.Unmarshal(res.Body.Bytes(), &run); err != nil || res.Code != http.StatusAccepted {
		t.Fatalf("start run: %d %s", res.Code, res.Body)
	}
	return run
}

func (f billingRunFixture) items(t *testing.T, runID string) map[string]api.BillingRunItemResponse {
	t.Helper()
	res := f.e.Do(t, http.MethodGet, "/v1/account/billing-runs/"+runID+"/items", nil, nil)
	var page api.BillingRunItemListResponse
	if err := json.Unmarshal(res.Body.Bytes(), &page); err != nil || res.Code != http.StatusOK {
		t.Fatalf("items: %d %s", res.Code, res.Body)
	}
	out := map[string]api.BillingRunItemResponse{}
	for _, item := range page.Items {
		out[item.ExternalRef] = item
	}
	return out
}

// adr: 1503 — one run bills every customer of an ended month: exported,
// held on unpriced usage, failed without a Stripe customer, skipped when a
// platform tenant bills the usage; running the month again exports nothing.
func TestBillingRunExportsEveryCustomer(t *testing.T) {
	f := newBillingRunFixture(t)
	acct := (*f.e.Acct()).ID
	apidserver.MustSeedApp(t, f.e, "priced")
	apidserver.MustSeedApp(t, f.e, "unpriced")
	if res := f.e.Do(t, http.MethodPost, "/v1/apps/priced/rate-cards", api.CreateAPIConsumerRateCardRequest{
		Currency: "EUR", PriceMillicentsPerUnit: 1250, EffectiveFrom: &f.month}, nil); res.Code != http.StatusCreated {
		t.Fatalf("rate card: %d %s", res.Code, res.Body)
	}
	tenant, _, err := (*f.e.Store()).CreatePlatformTenant(context.Background(), acct, "cus_Tenant1", "Tenant", 250)
	if err != nil {
		t.Fatal(err)
	}
	f.consumer(t, "priced", "cus_Paying1", 3, "")
	f.consumer(t, "priced", "no-stripe-id", 2, "")
	f.consumer(t, "unpriced", "cus_Unpriced1", 4, "")
	f.consumer(t, "priced", "cus_Linked1", 5, tenant.ID)

	if res := f.e.Do(t, http.MethodPost, "/v1/account/billing-runs", api.CreateBillingRunRequest{Month: f.month.AddDate(0, 1, 0).Format("2006-01")}, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("current month: %d %s, want 422", res.Code, res.Body)
	}
	month := f.month.Format("2006-01")
	run := f.start(t, api.CreateBillingRunRequest{Month: month, Export: "stripe"})
	if res := f.e.Do(t, http.MethodPost, "/v1/account/billing-runs", api.CreateBillingRunRequest{Month: month}, nil); res.Code != http.StatusConflict ||
		!strings.Contains(res.Body.String(), run.ID) {
		t.Fatalf("second run: %d %s, want 409 naming %s", res.Code, res.Body, run.ID)
	}
	done := f.drain(t, run.ID)
	if c := done.Counts; c.Exported != 2 || c.Held != 1 || c.Failed != 1 || c.Skipped != 1 || c.Pending != 0 {
		t.Fatalf("counts = %+v", c)
	}
	items := f.items(t, run.ID)
	paying := items["cus_Paying1"]
	if paying.Outcome != "exported" || paying.InvoiceID == "" || paying.AmountMillicents == nil || *paying.AmountMillicents != 3750 ||
		paying.Completeness == "" || paying.ExportedInvoices != 1 {
		t.Fatalf("paying consumer = %+v", paying)
	}
	if item := items["cus_Tenant1"]; item.Outcome != "exported" || item.PlatformTenantID != tenant.ID {
		t.Fatalf("tenant = %+v", item)
	}
	if item := items["cus_Unpriced1"]; item.Outcome != "held" || !strings.Contains(item.Reason, "rate card") {
		t.Fatalf("unpriced consumer = %+v", item)
	}
	if item := items["no-stripe-id"]; item.Outcome != "failed" || !strings.Contains(item.Reason, "Stripe customer") {
		t.Fatalf("consumer without a Stripe customer = %+v", item)
	}
	if item := items["cus_Linked1"]; item.Outcome != "skipped" || !strings.Contains(item.Reason, "platform tenant") {
		t.Fatalf("tenant-attributed consumer = %+v", item)
	}
	if f.stripe.invoices != 2 {
		t.Fatalf("stripe invoices = %d, want 2", f.stripe.invoices)
	}

	again := f.drain(t, f.start(t, api.CreateBillingRunRequest{Month: month, Export: "stripe"}).ID)
	if again.Counts.UpToDate != 2 || again.Counts.Exported != 0 || f.stripe.invoices != 2 {
		t.Fatalf("second run counts = %+v, stripe invoices = %d", again.Counts, f.stripe.invoices)
	}
	held := f.e.Do(t, http.MethodGet, "/v1/account/billing-runs/"+again.ID+"/items?outcome=held", nil, nil)
	if held.Code != http.StatusOK || strings.Count(held.Body.String(), `"outcome":"held"`) != 1 {
		t.Fatalf("held items: %d %s", held.Code, held.Body)
	}
	for _, path := range []string{"/v1/account/billing-runs/not-a-run", "/v1/account/billing-runs/not-a-run/items", "/v1/account/billing-runs/" + uuid.NewString()} {
		if res := f.e.Do(t, http.MethodGet, path, nil, nil); res.Code != http.StatusNotFound {
			t.Fatalf("GET %s: %d, want 404", path, res.Code)
		}
	}
	list := f.e.Do(t, http.MethodGet, "/v1/account/billing-runs?limit=1", nil, nil)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), again.ID) || strings.Contains(list.Body.String(), run.ID) {
		t.Fatalf("list: %d %s", list.Code, list.Body)
	}
}

// adr: 1503 — a schedule starts the month's run after month end plus its
// delay, once.
func TestBillingRunScheduleStartsMonthlyRun(t *testing.T) {
	f := newBillingRunFixture(t)
	if res := f.e.Do(t, http.MethodGet, "/v1/account/billing-run-schedule", nil, nil); res.Code != http.StatusNotFound {
		t.Fatalf("schedule before put: %d", res.Code)
	}
	delay := 0
	if res := f.e.Do(t, http.MethodPut, "/v1/account/billing-run-schedule", api.PutBillingRunScheduleRequest{DelayHours: &delay}, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("zero delay: %d %s", res.Code, res.Body)
	}
	delay = 6
	res := f.e.Do(t, http.MethodPut, "/v1/account/billing-run-schedule", api.PutBillingRunScheduleRequest{DelayHours: &delay}, nil)
	var sched api.BillingRunScheduleResponse
	if err := json.Unmarshal(res.Body.Bytes(), &sched); err != nil || res.Code != http.StatusOK {
		t.Fatalf("put schedule: %d %s", res.Code, res.Body)
	}
	current := f.month.AddDate(0, 1, 0)
	if sched.NextMonth != current.Format("2006-01") || !sched.NextRunAt.Equal(current.AddDate(0, 1, 0).Add(6*time.Hour)) {
		t.Fatalf("schedule = %+v", sched)
	}
	runs := func() api.BillingRunListResponse {
		res := f.e.Do(t, http.MethodGet, "/v1/account/billing-runs", nil, nil)
		var out api.BillingRunListResponse
		if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil || res.Code != http.StatusOK {
			t.Fatalf("list runs: %d %s", res.Code, res.Body)
		}
		return out
	}
	service := (*f.e.S()).BillingRuns()
	if err := service.RunOnce(context.Background()); err != nil || len(runs().Runs) != 0 {
		t.Fatalf("run before next_run_at: %v, %+v", err, runs())
	}
	service.Now = func() time.Time { return sched.NextRunAt.Add(time.Minute) }
	for range 3 {
		if err := service.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	got := runs()
	if len(got.Runs) != 1 || got.Runs[0].Trigger != "schedule" || got.Runs[0].Month != sched.NextMonth || got.Runs[0].Status != "completed" {
		t.Fatalf("scheduled runs = %+v", got)
	}
	res = f.e.Do(t, http.MethodGet, "/v1/account/billing-run-schedule", nil, nil)
	if err := json.Unmarshal(res.Body.Bytes(), &sched); err != nil || sched.LastMonth != current.Format("2006-01") ||
		sched.NextMonth != current.AddDate(0, 1, 0).Format("2006-01") {
		t.Fatalf("advanced schedule = %+v", sched)
	}
	if res := f.e.Do(t, http.MethodDelete, "/v1/account/billing-run-schedule", nil, nil); res.Code != http.StatusNoContent {
		t.Fatalf("delete schedule: %d", res.Code)
	}
}
