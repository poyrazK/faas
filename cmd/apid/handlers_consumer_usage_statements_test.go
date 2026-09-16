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
