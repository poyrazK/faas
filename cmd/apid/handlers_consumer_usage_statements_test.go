package main

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
