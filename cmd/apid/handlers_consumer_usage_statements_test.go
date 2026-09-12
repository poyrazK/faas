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
	finalizePath := path + "/" + statement.ID + "/finalize"
	finalized := e.do(t, http.MethodPost, finalizePath, struct{}{}, nil)
	if finalized.Code != http.StatusOK {
		t.Fatalf("finalize: %d %s", finalized.Code, finalized.Body)
	}
	var final api.APIConsumerUsageStatementResponse
	if err := json.Unmarshal(finalized.Body.Bytes(), &final); err != nil || final.Status != "finalized" || final.FinalizedAt == nil {
		t.Fatalf("final statement = %+v err=%v", final, err)
	}
	repeated := e.do(t, http.MethodPost, finalizePath, struct{}{}, nil)
	if repeated.Code != http.StatusOK {
		t.Fatalf("repeat finalize: %d %s", repeated.Code, repeated.Body)
	}
	listed := e.do(t, http.MethodGet, path, nil, nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("list statements: %d %s", listed.Code, listed.Body)
	}
}
