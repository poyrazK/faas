package meter_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/polar"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// This narrow fault boundary mirrors a DB failure after Polar accepted the event.
type costControlDedupeWriteFailure struct {
	written  bool
	attempts int
}

func (d *costControlDedupeWriteFailure) HasStripePushHour(context.Context, string, time.Time) (bool, error) {
	return d.written, nil
}
func (d *costControlDedupeWriteFailure) RecordStripePushHour(context.Context, string, time.Time) error {
	d.attempts++
	if d.attempts == 1 {
		return errors.New("audit: local dedupe persistence unavailable")
	}
	d.written = true
	return nil
}

func TestCostControlRetryKeepsAcceptedComputeQuantity(t *testing.T) {
	store := state.NewMemStore()
	acct := makeAccount(t, t.Context(), store, api.PlanHobby)
	if err := store.UpsertBillingIdentity(t.Context(), state.BillingIdentity{AccountID: acct.ID, Provider: "polar", CustomerID: "audit-customer", SubscriptionID: "audit-subscription", BillingFrom: time.Unix(0, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	store.SetOverageCapCentsForTest(acct.ID, 20)
	raw := api.IncludedMBSeconds(api.PlanHobby) + 100*api.SecondsPerGBHour
	if err := store.AppendUsage(t.Context(), acct.ID, "audit-app", "audit-instance", now.Add(-time.Hour), raw, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	completeUsageThrough(t, t.Context(), store, now)
	var mu sync.Mutex
	accepted := map[string]int64{}
	var attempts []int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/events/ingest" {
			t.Errorf("unexpected provider path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var body struct {
			Events []struct {
				ExternalID string `json:"external_id"`
				Metadata   struct {
					MBSeconds int64 `json:"mb_seconds"`
				} `json:"metadata"`
			} `json:"events"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Events) != 1 {
			t.Errorf("invalid event body: %v", err)
			w.WriteHeader(400)
			return
		}
		event := body.Events[0]
		mu.Lock()
		defer mu.Unlock()
		attempts = append(attempts, event.Metadata.MBSeconds)
		if _, ok := accepted[event.ExternalID]; ok {
			w.Write([]byte(`{"inserted":0,"duplicates":1}`))
			return
		}
		accepted[event.ExternalID] = event.Metadata.MBSeconds
		w.Write([]byte(`{"inserted":1,"duplicates":0}`))
	}))
	defer server.Close()
	dedupe := &costControlDedupeWriteFailure{}
	provider, err := polar.NewProviderWithDedupe(polar.Config{APIKey: "audit-local", BaseURL: server.URL}, discardLog(), dedupe)
	if err != nil {
		t.Fatal(err)
	}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)
	if pushed, err := pusher.PushPending(t.Context(), 24*time.Hour); err == nil || pushed != 0 {
		t.Fatalf("first push=(%d,%v), want persistence failure", pushed, err)
	}
	store.SetOverageCapCentsForTest(acct.ID, 50)
	if pushed, err := pusher.PushPending(t.Context(), 24*time.Hour); err != nil || pushed != 1 {
		t.Fatalf("retry=(%d,%v)", pushed, err)
	}
	recorded, err := store.DeliveredComputeMBSeconds(t.Context(), "polar", acct.ID, now.Add(-time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	var providerTotal int64
	for _, q := range accepted {
		providerTotal += q
	}
	if len(attempts) != 2 || len(accepted) != 1 {
		t.Fatalf("attempts=%v accepted=%v", attempts, accepted)
	}
	if recorded != providerTotal {
		t.Errorf("same hourly external ID retried with changed quantity: attempts=%v GB-h; provider accepted=%d GB-h; local receipt=%d GB-h", []int64{attempts[0] / api.SecondsPerGBHour, attempts[1] / api.SecondsPerGBHour}, providerTotal/api.SecondsPerGBHour, recorded/api.SecondsPerGBHour)
	}
}

// The last UTC minute cannot be sampled before its month has closed. A
// monitored threshold must still be eligible when that evidence arrives.
func TestCostControlBudgetAlertsForFinalMinuteOfClosedMonth(t *testing.T) {
	store := state.NewMemStore()
	acct := makeAccount(t, t.Context(), store, api.PlanHobby)
	month := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	rollover := month.AddDate(0, 1, 0)
	now := rollover.Add(-time.Minute)
	_, err := store.PutFinancialPriceSnapshot(t.Context(), state.FinancialPriceSnapshot{AccountID: acct.ID, PeriodStart: month, PeriodEnd: rollover, EffectiveFrom: month, Plan: api.PlanHobby, DeliveryMode: "live", Price: financial.Price{Version: "audit-month-end", Meter: "compute", Currency: "USD", Unit: "mb_seconds", UnitQuantity: 1, MillicentsPerUnit: 1}})
	if err != nil {
		t.Fatal(err)
	}
	spec := financial.BudgetSpec{Name: "last minute", Scope: financial.BudgetScope{Kind: "account"}, Currency: "USD", Meters: []string{"compute"}, Basis: "gross_usage", LimitMillicents: 1000, Mode: "monitored", Action: "notify", ResumeRule: "next_period", Enabled: true}
	store.SetClockForTest(func() time.Time { return now })
	policy, err := store.CreateFinancialBudget(t.Context(), acct.ID, uuid.NewString(), "audit", spec)
	if err != nil {
		t.Fatal(err)
	}
	loop := meter.NewLoop(store, nil, nil, nil, nil, &fakeMailer{}, nil, nil, nil, func() time.Time { return now }, discardLog(), nil, nil)
	if err := loop.RunFinancialBudgetsOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	// The sampler writes October's 23:59 interval only after November starts.
	now = rollover.Add(time.Minute)
	if err := store.AppendUsage(t.Context(), acct.ID, uuid.NewString(), uuid.NewString(), rollover.Add(-time.Minute), 1000, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordFinancialSamplingWindow(t.Context(), rollover.Add(-time.Minute), true, false); err != nil {
		t.Fatal(err)
	}
	report, err := billing.ReadFinancialCosts(t.Context(), store, acct.ID, month, now)
	if err != nil {
		t.Fatal(err)
	}
	evaluation, err := billing.EvaluateFinancialBudget(report, spec, now)
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.KnownMillicents == nil || *evaluation.KnownMillicents != 1000 || !evaluation.Fresh {
		t.Fatalf("fixture does not cross a fresh October threshold: %+v", evaluation)
	}
	if err := loop.RunFinancialBudgetsOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	runtime, err := store.FinancialBudgetRuntime(t.Context(), acct.ID, policy.ID, policy.Revision, month)
	if err != nil {
		t.Fatal(err)
	}
	if len(runtime.Alerts) == 0 {
		t.Errorf("October's final minute reaches %d millicents, but after the UTC rollover the monitor only evaluates November and retains no October threshold alert", *evaluation.KnownMillicents)
	}
}

func TestCostControlEditedPolicyDoesNotAlertForPriorMonth(t *testing.T) {
	store := state.NewMemStore()
	acct := makeAccount(t, t.Context(), store, api.PlanHobby)
	end := financial.ComputeMonth(time.Now())
	month := end.AddDate(0, -1, 0)
	now := end.Add(-time.Minute)
	store.SetClockForTest(func() time.Time { return now })
	_, err := store.PutFinancialPriceSnapshot(t.Context(), state.FinancialPriceSnapshot{AccountID: acct.ID, PeriodStart: month, PeriodEnd: end, EffectiveFrom: month, Plan: api.PlanHobby, DeliveryMode: "live", Price: financial.Price{Version: "old-policy-month", Meter: "compute", Currency: "USD", Unit: "mb_seconds", UnitQuantity: 1, MillicentsPerUnit: 1}})
	if err != nil {
		t.Fatal(err)
	}
	spec := financial.BudgetSpec{Name: "old revision", Scope: financial.BudgetScope{Kind: "account"}, Currency: "USD", Meters: []string{"compute"}, Basis: "gross_usage", LimitMillicents: 1000, Mode: "monitored", Action: "notify", ResumeRule: "next_period", Enabled: true}
	p, err := store.CreateFinancialBudget(t.Context(), acct.ID, uuid.NewString(), "test", spec)
	if err != nil {
		t.Fatal(err)
	}
	now = end.Add(time.Minute)
	if err := store.AppendUsage(t.Context(), acct.ID, uuid.NewString(), uuid.NewString(), end.Add(-time.Minute), 1000, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordFinancialSamplingWindow(t.Context(), end.Add(-time.Minute), true, false); err != nil {
		t.Fatal(err)
	}
	spec.Name = "edited after close"
	p, err = store.UpdateFinancialBudget(t.Context(), acct.ID, p.ID, p.Revision, "test", spec)
	if err != nil {
		t.Fatal(err)
	}
	loop := meter.NewLoop(store, nil, nil, nil, nil, &fakeMailer{}, nil, nil, nil, func() time.Time { return now }, discardLog(), nil, nil)
	if err := loop.RunFinancialBudgetsOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	prior, err := store.FinancialBudgetRuntime(t.Context(), acct.ID, p.ID, p.Revision, month)
	if err != nil || len(prior.Alerts) != 0 {
		t.Fatalf("edited revision alerted for prior month: %+v %v", prior, err)
	}
	current, err := store.FinancialBudgetRuntime(t.Context(), acct.ID, p.ID, p.Revision, end)
	if err != nil || current.Evaluation == nil {
		t.Fatalf("current monitoring missing: %+v %v", current, err)
	}
}
