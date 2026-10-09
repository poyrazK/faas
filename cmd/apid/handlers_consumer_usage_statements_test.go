package main

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 120 — customer API consumer metering, pricing, and monetization foundation.
func TestAPIConsumerUsageStatementSnapshotAndFinalize(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "consumer-statements")
	created := e.do(t, http.MethodPost, "/v1/apps/consumer-statements/consumers", api.CreateAPIConsumerRequest{
		ExternalRef: "statement-customer", Name: "Statement Customer",
	}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create consumer: %d %s", created.Code, created.Body)
	}
	var consumer api.APIConsumerResponse
	if err := json.Unmarshal(created.Body.Bytes(), &consumer); err != nil {
		t.Fatal(err)
	}
	minute := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Minute)
	rate := e.do(t, http.MethodPost, "/v1/apps/consumer-statements/rate-cards", api.CreateAPIConsumerRateCardRequest{
		Currency: "EUR", PriceMillicentsPerUnit: 25, EffectiveFrom: &minute,
	}, nil)
	if rate.Code != http.StatusCreated {
		t.Fatalf("create rate card: %d %s", rate.Code, rate.Body)
	}
	if _, err := e.store.RecordAPIConsumerUsage(context.Background(), state.APIConsumerUsageEvent{
		EventID: uuid.NewString(), AccountID: e.acct.ID, AppID: consumer.AppID,
		ConsumerKey: consumer.ID, WindowStart: minute, RequestCount: 3, BillableUnits: 3,
	}); err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/consumer-statements/consumers/" + consumer.ID + "/usage-statements"
	period := api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &minute, PeriodEnd: func() *time.Time { v := minute.Add(time.Hour); return &v }()}
	first := e.do(t, http.MethodPost, path, period, nil)
	if first.Code != http.StatusCreated {
		t.Fatalf("create statement: %d %s", first.Code, first.Body)
	}
	var statement api.APIConsumerUsageStatementResponse
	if err := json.Unmarshal(first.Body.Bytes(), &statement); err != nil {
		t.Fatal(err)
	}
	if statement.Status != "draft" || !statement.Priced || statement.AmountMillicents != 75 || statement.UnpricedUnits != 0 || len(statement.Buckets) != 1 {
		t.Fatalf("statement = %+v", statement)
	}
	duplicate := e.do(t, http.MethodPost, path, period, nil)
	if duplicate.Code != http.StatusOK {
		t.Fatalf("duplicate statement: %d %s", duplicate.Code, duplicate.Body)
	}
	var replay api.APIConsumerUsageStatementResponse
	if err := json.Unmarshal(duplicate.Body.Bytes(), &replay); err != nil || replay.ID != statement.ID {
		t.Fatalf("duplicate replay = %+v err=%v", replay, err)
	}
	hook, err := e.store.CreateAppWebhook(context.Background(), state.AppWebhook{
		AppID: consumer.AppID, AccountID: e.acct.ID, TargetURL: "https://billing.example/statements",
		EventFilter: []string{string(state.AppWebhookEventUsageStatementFinalized)}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	finalizePath := path + "/" + statement.ID + "/finalize"
	finalized := e.do(t, http.MethodPost, finalizePath, struct{}{}, nil)
	if finalized.Code != http.StatusOK {
		t.Fatalf("finalize: %d %s", finalized.Code, finalized.Body)
	}
	var final api.APIConsumerUsageStatementResponse
	if err := json.Unmarshal(finalized.Body.Bytes(), &final); err != nil || final.Status != "finalized" || final.FinalizedAt == nil {
		t.Fatalf("final statement = %+v err=%v", final, err)
	}
	deliveries, _, err := e.store.ListAppWebhookDeliveries(context.Background(), consumer.AppID, hook.ID, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 || deliveries[0].Event != state.AppWebhookEventUsageStatementFinalized {
		t.Fatalf("deliveries = %+v, want one finalized statement delivery", deliveries)
	}
	var delivery api.APIConsumerUsageStatementFinalizedWebhookPayload
	if err := json.Unmarshal(deliveries[0].Payload, &delivery); err != nil {
		t.Fatal(err)
	}
	if delivery.StatementID != statement.ID || delivery.ConsumerID != consumer.ID || delivery.AmountMillicents != 75 || delivery.FinalizedAt.IsZero() {
		t.Fatalf("delivery payload = %+v", delivery)
	}
	repeated := e.do(t, http.MethodPost, finalizePath, struct{}{}, nil)
	if repeated.Code != http.StatusOK {
		t.Fatalf("repeat finalize: %d %s", repeated.Code, repeated.Body)
	}
	deliveries, _, err = e.store.ListAppWebhookDeliveries(context.Background(), consumer.AppID, hook.ID, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("repeat finalize enqueued %d deliveries, want one", len(deliveries))
	}
	handoffPath := finalizePath[:len(finalizePath)-len("/finalize")] + "/handoff"
	claim := e.do(t, http.MethodPost, handoffPath, api.ClaimAPIConsumerUsageStatementRequest{ExternalInvoiceID: "customer-invoice-1001"}, nil)
	if claim.Code != http.StatusCreated {
		t.Fatalf("claim handoff: %d %s", claim.Code, claim.Body)
	}
	var handoff api.APIConsumerUsageStatementHandoffResponse
	if err := json.Unmarshal(claim.Body.Bytes(), &handoff); err != nil || handoff.StatementID != statement.ID || handoff.ExternalInvoiceID != "customer-invoice-1001" || handoff.AmountMillicents != 75 {
		t.Fatalf("handoff = %+v err=%v", handoff, err)
	}
	replayedClaim := e.do(t, http.MethodPost, handoffPath, api.ClaimAPIConsumerUsageStatementRequest{ExternalInvoiceID: "customer-invoice-1001"}, nil)
	if replayedClaim.Code != http.StatusOK {
		t.Fatalf("replay handoff: %d %s", replayedClaim.Code, replayedClaim.Body)
	}
	var replayedHandoff api.APIConsumerUsageStatementHandoffResponse
	if err := json.Unmarshal(replayedClaim.Body.Bytes(), &replayedHandoff); err != nil || replayedHandoff.ID != handoff.ID {
		t.Fatalf("replayed handoff = %+v err=%v", replayedHandoff, err)
	}
	conflictingClaim := e.do(t, http.MethodPost, handoffPath, api.ClaimAPIConsumerUsageStatementRequest{ExternalInvoiceID: "customer-invoice-1002"}, nil)
	if conflictingClaim.Code != http.StatusConflict {
		t.Fatalf("conflicting handoff: %d %s", conflictingClaim.Code, conflictingClaim.Body)
	}
	gotHandoff := e.do(t, http.MethodGet, handoffPath, nil, nil)
	if gotHandoff.Code != http.StatusOK {
		t.Fatalf("get handoff: %d %s", gotHandoff.Code, gotHandoff.Body)
	}
	listed := e.do(t, http.MethodGet, path, nil, nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("list statements: %d %s", listed.Code, listed.Body)
	}
}

// adr: 843 — app-local statements supersede changed drafts and bill late
// usage as additive adjustment revisions of the same period.
func TestAPIConsumerUsageStatementRevisionsAndLateUsage(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "consumer-revisions")
	created := e.do(t, http.MethodPost, "/v1/apps/consumer-revisions/consumers", api.CreateAPIConsumerRequest{
		ExternalRef: "revision-customer", Name: "Revision Customer",
	}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create consumer: %d %s", created.Code, created.Body)
	}
	var consumer api.APIConsumerResponse
	if err := json.Unmarshal(created.Body.Bytes(), &consumer); err != nil {
		t.Fatal(err)
	}
	minute := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Minute)
	if rate := e.do(t, http.MethodPost, "/v1/apps/consumer-revisions/rate-cards", api.CreateAPIConsumerRateCardRequest{
		Currency: "EUR", PriceMillicentsPerUnit: 10, EffectiveFrom: &minute,
	}, nil); rate.Code != http.StatusCreated {
		t.Fatalf("create rate card: %d %s", rate.Code, rate.Body)
	}
	recordUsage := func(at time.Time, units int64) {
		t.Helper()
		if _, err := e.store.RecordAPIConsumerUsage(context.Background(), state.APIConsumerUsageEvent{
			EventID: uuid.NewString(), AccountID: e.acct.ID, AppID: consumer.AppID,
			ConsumerKey: consumer.ID, WindowStart: at, RequestCount: units, BillableUnits: units,
		}); err != nil {
			t.Fatal(err)
		}
	}
	path := "/v1/apps/consumer-revisions/consumers/" + consumer.ID + "/usage-statements"
	end := minute.Add(time.Hour)
	period := api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &minute, PeriodEnd: &end}
	snapshot := func(wantCode int) api.APIConsumerUsageStatementResponse {
		t.Helper()
		res := e.do(t, http.MethodPost, path, period, nil)
		if res.Code != wantCode {
			t.Fatalf("create statement: %d %s, want %d", res.Code, res.Body, wantCode)
		}
		var out api.APIConsumerUsageStatementResponse
		if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	post := func(target string, body any, wantCode int) {
		t.Helper()
		if res := e.do(t, http.MethodPost, target, body, nil); res.Code != wantCode {
			t.Fatalf("POST %s: %d %s, want %d", target, res.Code, res.Body, wantCode)
		}
	}

	recordUsage(minute, 2)
	draft := snapshot(http.StatusCreated)
	if draft.Revision != 1 || draft.Status != "draft" || draft.BillableUnits != 2 {
		t.Fatalf("first draft = %+v", draft)
	}
	if replay := snapshot(http.StatusOK); replay.ID != draft.ID {
		t.Fatalf("unchanged draft replay = %+v, want %s", replay, draft.ID)
	}

	// Usage arriving while the draft is open supersedes it.
	recordUsage(minute.Add(time.Minute), 3)
	refreshed := snapshot(http.StatusCreated)
	if refreshed.Revision != 2 || refreshed.Status != "draft" || refreshed.BillableUnits != 5 || refreshed.AmountMillicents != 50 {
		t.Fatalf("refreshed draft = %+v", refreshed)
	}
	var stale api.APIConsumerUsageStatementResponse
	if err := json.Unmarshal(e.do(t, http.MethodGet, path+"/"+draft.ID, nil, nil).Body.Bytes(), &stale); err != nil || stale.Status != "superseded" {
		t.Fatalf("superseded draft = %+v err=%v", stale, err)
	}
	post(path+"/"+draft.ID+"/finalize", struct{}{}, http.StatusConflict)
	post(path+"/"+refreshed.ID+"/finalize", struct{}{}, http.StatusOK)
	post(path+"/"+refreshed.ID+"/handoff", api.ClaimAPIConsumerUsageStatementRequest{ExternalInvoiceID: "inv-1"}, http.StatusCreated)
	if replay := snapshot(http.StatusOK); replay.ID != refreshed.ID {
		t.Fatalf("finalized replay without new usage = %+v", replay)
	}

	// Late usage, including more units in an already-billed minute, becomes
	// an adjustment that carries only the uncovered units.
	recordUsage(minute, 1)
	recordUsage(minute.Add(2*time.Minute), 4)
	adjustment := snapshot(http.StatusCreated)
	if adjustment.Revision != 3 || adjustment.Status != "draft" || adjustment.BillableUnits != 5 || adjustment.AmountMillicents != 50 || len(adjustment.Buckets) != 2 {
		t.Fatalf("adjustment = %+v", adjustment)
	}
	post(path+"/"+adjustment.ID+"/finalize", struct{}{}, http.StatusOK)
	post(path+"/"+adjustment.ID+"/handoff", api.ClaimAPIConsumerUsageStatementRequest{ExternalInvoiceID: "inv-1"}, http.StatusConflict)
	post(path+"/"+adjustment.ID+"/handoff", api.ClaimAPIConsumerUsageStatementRequest{ExternalInvoiceID: "inv-2"}, http.StatusCreated)

	// A different period overlapping billed usage still cannot be handed off.
	overlapStart, overlapEnd := minute.Add(time.Minute), minute.Add(2*time.Hour)
	res := e.do(t, http.MethodPost, path, api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &overlapStart, PeriodEnd: &overlapEnd}, nil)
	var overlap api.APIConsumerUsageStatementResponse
	if err := json.Unmarshal(res.Body.Bytes(), &overlap); err != nil || res.Code != http.StatusCreated {
		t.Fatalf("overlapping statement: %d %s", res.Code, res.Body)
	}
	post(path+"/"+overlap.ID+"/finalize", struct{}{}, http.StatusOK)
	post(path+"/"+overlap.ID+"/handoff", api.ClaimAPIConsumerUsageStatementRequest{ExternalInvoiceID: "inv-3"}, http.StatusConflict)
}

// adr: 844 — a rate card's monthly allowance is free, cannot be backdated,
// and late usage that exhausts it sooner is billed as an adjustment.
func TestAPIConsumerRateCardAllowanceInStatements(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	if now.Add(10*time.Minute).Month() != now.Month() {
		t.Skip("allowance months must not roll over during the test")
	}
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "consumer-allowance")
	created := e.do(t, http.MethodPost, "/v1/apps/consumer-allowance/consumers", api.CreateAPIConsumerRequest{
		ExternalRef: "allowance-customer", Name: "Allowance Customer",
	}, nil)
	var consumer api.APIConsumerResponse
	if err := json.Unmarshal(created.Body.Bytes(), &consumer); err != nil || created.Code != http.StatusCreated {
		t.Fatalf("create consumer: %d %s", created.Code, created.Body)
	}
	cards := "/v1/apps/consumer-allowance/rate-cards"
	past := now.Add(-time.Hour)
	if res := e.do(t, http.MethodPost, cards, api.CreateAPIConsumerRateCardRequest{
		Currency: "EUR", PriceMillicentsPerUnit: 10, IncludedUnitsPerMonth: 5, EffectiveFrom: &past,
	}, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("backdated allowance card: %d %s, want 422", res.Code, res.Body)
	}
	res := e.do(t, http.MethodPost, cards, api.CreateAPIConsumerRateCardRequest{
		Currency: "EUR", PriceMillicentsPerUnit: 10, IncludedUnitsPerMonth: 5, EffectiveFrom: &now,
	}, nil)
	var card api.APIConsumerRateCardResponse
	if err := json.Unmarshal(res.Body.Bytes(), &card); err != nil || res.Code != http.StatusCreated || card.IncludedUnitsPerMonth != 5 {
		t.Fatalf("allowance card: %d %s", res.Code, res.Body)
	}
	m0, m1 := now.Add(time.Minute), now.Add(2*time.Minute)
	record := func(at time.Time, units int64) {
		t.Helper()
		if _, err := e.store.RecordAPIConsumerUsage(context.Background(), state.APIConsumerUsageEvent{
			EventID: uuid.NewString(), AccountID: e.acct.ID, AppID: consumer.AppID,
			ConsumerKey: consumer.ID, WindowStart: at, RequestCount: units, BillableUnits: units,
		}); err != nil {
			t.Fatal(err)
		}
	}
	path := "/v1/apps/consumer-allowance/consumers/" + consumer.ID + "/usage-statements"
	end := now.Add(time.Hour)
	draft := func() api.APIConsumerUsageStatementResponse {
		t.Helper()
		res := e.do(t, http.MethodPost, path, api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &now, PeriodEnd: &end}, nil)
		var out api.APIConsumerUsageStatementResponse
		if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil || res.Code != http.StatusCreated {
			t.Fatalf("draft: %d %s", res.Code, res.Body)
		}
		return out
	}

	record(m0, 3)
	record(m1, 4)
	first := draft()
	if first.BillableUnits != 7 || first.AmountMillicents != 20 || len(first.Buckets) != 2 ||
		first.Buckets[0].ChargedUnits != 0 || first.Buckets[1].ChargedUnits != 2 {
		t.Fatalf("first statement = %+v, want 5 free and 2 charged units", first)
	}
	if res := e.do(t, http.MethodPost, path+"/"+first.ID+"/finalize", struct{}{}, nil); res.Code != http.StatusOK {
		t.Fatalf("finalize: %d %s", res.Code, res.Body)
	}

	// Two late units in the first minute are free, but the second minute
	// now exceeds the allowance by two more units.
	record(m0, 2)
	adjustment := draft()
	if adjustment.Revision != 2 || adjustment.BillableUnits != 2 || adjustment.AmountMillicents != 20 || len(adjustment.Buckets) != 2 ||
		adjustment.Buckets[0].ChargedUnits != 0 || adjustment.Buckets[1].BillableUnits != 0 || adjustment.Buckets[1].ChargedUnits != 2 {
		t.Fatalf("adjustment = %+v, want 2 added units and 2 newly charged units", adjustment)
	}
}

// adr: 845 — graduated tiers price monthly statements by position and
// re-rate late usage exactly.
func TestAPIConsumerRateCardTiersInStatements(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	if now.Add(10*time.Minute).Month() != now.Month() {
		t.Skip("tier months must not roll over during the test")
	}
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "consumer-tiers")
	created := e.do(t, http.MethodPost, "/v1/apps/consumer-tiers/consumers", api.CreateAPIConsumerRequest{
		ExternalRef: "tier-customer", Name: "Tier Customer",
	}, nil)
	var consumer api.APIConsumerResponse
	if err := json.Unmarshal(created.Body.Bytes(), &consumer); err != nil || created.Code != http.StatusCreated {
		t.Fatalf("create consumer: %d %s", created.Code, created.Body)
	}
	step := func(n int64) *int64 { return &n }
	cards := "/v1/apps/consumer-tiers/rate-cards"
	if res := e.do(t, http.MethodPost, cards, api.CreateAPIConsumerRateCardRequest{Currency: "EUR", EffectiveFrom: &now,
		Tiers: []api.APIConsumerRateCardTier{{UpTo: step(5), PriceMillicentsPerUnit: 10}, {PriceMillicentsPerUnit: 0}},
	}, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("free later step: %d %s, want 422", res.Code, res.Body)
	}
	res := e.do(t, http.MethodPost, cards, api.CreateAPIConsumerRateCardRequest{Currency: "EUR", EffectiveFrom: &now,
		Tiers: []api.APIConsumerRateCardTier{{UpTo: step(5), PriceMillicentsPerUnit: 0}, {UpTo: step(10), PriceMillicentsPerUnit: 100}, {PriceMillicentsPerUnit: 10}},
	}, nil)
	var card api.APIConsumerRateCardResponse
	if err := json.Unmarshal(res.Body.Bytes(), &card); err != nil || res.Code != http.StatusCreated || len(card.Tiers) != 3 {
		t.Fatalf("tiered card: %d %s", res.Code, res.Body)
	}
	m0, m1 := now.Add(time.Minute), now.Add(2*time.Minute)
	record := func(at time.Time, units int64) {
		t.Helper()
		if _, err := e.store.RecordAPIConsumerUsage(context.Background(), state.APIConsumerUsageEvent{
			EventID: uuid.NewString(), AccountID: e.acct.ID, AppID: consumer.AppID,
			ConsumerKey: consumer.ID, WindowStart: at, RequestCount: units, BillableUnits: units,
		}); err != nil {
			t.Fatal(err)
		}
	}
	path := "/v1/apps/consumer-tiers/consumers/" + consumer.ID + "/usage-statements"
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, 0)
	week := monthStart.AddDate(0, 0, 7)
	if res := e.do(t, http.MethodPost, path, api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &monthStart, PeriodEnd: &week}, nil); res.Code != http.StatusUnprocessableEntity && now.Before(week) {
		t.Fatalf("weekly tiered statement: %d %s, want 422", res.Code, res.Body)
	}
	draft := func() api.APIConsumerUsageStatementResponse {
		t.Helper()
		res := e.do(t, http.MethodPost, path, api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &monthStart, PeriodEnd: &monthEnd}, nil)
		var out api.APIConsumerUsageStatementResponse
		if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil || res.Code != http.StatusCreated {
			t.Fatalf("draft: %d %s", res.Code, res.Body)
		}
		return out
	}

	record(m0, 4)
	record(m1, 8)
	first := draft()
	if first.AmountMillicents != 520 || len(first.Buckets) != 2 || !slices.Equal(first.Buckets[1].TierUnits, []int64{1, 5, 2}) {
		t.Fatalf("first statement = %+v, want 1 free, 5 at 100 and 2 at 10 in the second minute", first)
	}
	if res := e.do(t, http.MethodPost, path+"/"+first.ID+"/finalize", struct{}{}, nil); res.Code != http.StatusOK {
		t.Fatalf("finalize: %d %s", res.Code, res.Body)
	}
	record(m0, 4)
	adjustment := draft()
	if adjustment.Revision != 2 || adjustment.AmountMillicents != 40 || len(adjustment.Buckets) != 2 ||
		adjustment.Buckets[1].AmountMillicents != -260 || !slices.Equal(adjustment.Buckets[1].TierUnits, []int64{-1, -3, 4}) {
		t.Fatalf("adjustment = %+v, want a 40 net re-rating", adjustment)
	}
	if res := e.do(t, http.MethodPost, path+"/"+adjustment.ID+"/finalize", struct{}{}, nil); res.Code != http.StatusOK {
		t.Fatalf("finalize adjustment: %d %s", res.Code, res.Body)
	}
}

// adr: 846 — route weights turn requests into weighted units before
// allowances and prices apply.
func TestAPIConsumerRateCardRouteWeightsInStatements(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	if now.Add(10*time.Minute).Month() != now.Month() {
		t.Skip("allowance months must not roll over during the test")
	}
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "consumer-weights")
	created := e.do(t, http.MethodPost, "/v1/apps/consumer-weights/consumers", api.CreateAPIConsumerRequest{
		ExternalRef: "weight-customer", Name: "Weight Customer",
	}, nil)
	var consumer api.APIConsumerResponse
	if err := json.Unmarshal(created.Body.Bytes(), &consumer); err != nil || created.Code != http.StatusCreated {
		t.Fatalf("create consumer: %d %s", created.Code, created.Body)
	}
	cards := "/v1/apps/consumer-weights/rate-cards"
	if res := e.do(t, http.MethodPost, cards, api.CreateAPIConsumerRateCardRequest{Currency: "EUR", PriceMillicentsPerUnit: 10, EffectiveFrom: &now,
		RouteWeights: map[string]int64{"/generate": 20},
	}, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("route without method: %d %s, want 422", res.Code, res.Body)
	}
	res := e.do(t, http.MethodPost, cards, api.CreateAPIConsumerRateCardRequest{Currency: "EUR", PriceMillicentsPerUnit: 10,
		IncludedUnitsPerMonth: 10, RouteWeights: map[string]int64{"POST /generate": 20}, EffectiveFrom: &now,
	}, nil)
	var card api.APIConsumerRateCardResponse
	if err := json.Unmarshal(res.Body.Bytes(), &card); err != nil || res.Code != http.StatusCreated || card.RouteWeights["POST /generate"] != 20 {
		t.Fatalf("weighted card: %d %s", res.Code, res.Body)
	}
	minute := now.Add(time.Minute)
	for _, route := range []string{"POST /generate", "POST /generate", "GET /items", "GET /items", "GET /items"} {
		if _, err := e.store.RecordAPIConsumerUsage(context.Background(), state.APIConsumerUsageEvent{
			EventID: uuid.NewString(), AccountID: e.acct.ID, AppID: consumer.AppID, ConsumerKey: consumer.ID,
			WindowStart: minute, RequestCount: 1, BillableUnits: 1, BillingRoute: route,
		}); err != nil {
			t.Fatal(err)
		}
	}
	end := now.Add(time.Hour)
	res = e.do(t, http.MethodPost, "/v1/apps/consumer-weights/consumers/"+consumer.ID+"/usage-statements",
		api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &now, PeriodEnd: &end}, nil)
	var statement api.APIConsumerUsageStatementResponse
	if err := json.Unmarshal(res.Body.Bytes(), &statement); err != nil || res.Code != http.StatusCreated {
		t.Fatalf("statement: %d %s", res.Code, res.Body)
	}
	// 2 x 20 + 3 x 1 = 43 weighted units; 10 are included, 33 cost 10 each.
	if statement.BillableUnits != 43 || statement.AmountMillicents != 330 {
		t.Fatalf("statement = %+v, want 43 weighted units and 330 millicents", statement)
	}
}
