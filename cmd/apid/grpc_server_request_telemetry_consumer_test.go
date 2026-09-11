package main

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type consumerTelemetryStore struct {
	account  state.Account
	inserted []sqlc.InsertRequestTelemetryParams
	usage    []state.APIConsumerUsageEvent
}

func (s *consumerTelemetryStore) AccountByID(context.Context, string) (state.Account, error) {
	return s.account, nil
}

func (s *consumerTelemetryStore) InsertRequestTelemetry(_ context.Context, arg sqlc.InsertRequestTelemetryParams) error {
	s.inserted = append(s.inserted, arg)
	return nil
}

func (s *consumerTelemetryStore) RecordAPIConsumerUsage(_ context.Context, event state.APIConsumerUsageEvent) (bool, error) {
	for _, existing := range s.usage {
		if existing.EventID == event.EventID {
			return false, nil
		}
	}
	s.usage = append(s.usage, event)
	return true, nil
}

func TestRequestTelemetryReceiverPersistsConsumerID(t *testing.T) {
	store := &consumerTelemetryStore{account: state.Account{Plan: api.PlanPro}}
	r := newRequestTelemetryReceiver(store, nil, nil, true)
	accountID, appID, deploymentID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	consumerID := uuid.NewString()

	out := r.handleOne(context.Background(), &apidpb.IncrementRequestTelemetryRequest{
		AccountId: accountID, AppId: appID, DeploymentId: deploymentID,
		RouteTemplate: "GET /v1/usage", Method: "GET", HttpStatus: 200,
		LatencyMs: 20, ReceivedAtUnixMs: 1, ConsumerId: consumerID,
	})
	if out.GetOutcome() != rtOutcomeInserted {
		t.Fatalf("outcome = %q, want %q", out.GetOutcome(), rtOutcomeInserted)
	}
	if len(store.inserted) != 1 {
		t.Fatalf("insert count = %d, want 1", len(store.inserted))
	}
	if got := store.inserted[0].ConsumerID.String(); got != consumerID {
		t.Fatalf("ConsumerID = %q, want %q", got, consumerID)
	}
}

func TestRequestTelemetryReceiverRejectsMalformedConsumerID(t *testing.T) {
	store := &consumerTelemetryStore{account: state.Account{Plan: api.PlanPro}}
	r := newRequestTelemetryReceiver(store, nil, nil, true)
	out := r.handleOne(context.Background(), &apidpb.IncrementRequestTelemetryRequest{
		AccountId: uuid.NewString(), AppId: uuid.NewString(), DeploymentId: uuid.NewString(),
		RouteTemplate: "GET /v1/usage", Method: "GET", HttpStatus: 200,
		LatencyMs: 20, ReceivedAtUnixMs: 1, ConsumerId: "not-a-uuid",
	})
	if out.GetOutcome() != rtOutcomeDBError {
		t.Fatalf("outcome = %q, want %q", out.GetOutcome(), rtOutcomeDBError)
	}
	if len(store.inserted) != 0 {
		t.Fatal("malformed consumer identity must not reach the database")
	}
}

func TestRequestTelemetryReceiverRecordsDurableConsumerUsageBeforeTelemetryGate(t *testing.T) {
	store := &consumerTelemetryStore{account: state.Account{Plan: api.PlanFree}}
	r := newRequestTelemetryReceiver(store, nil, nil, true)
	accountID, appID, deploymentID, eventID, consumerID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	req := &apidpb.IncrementRequestTelemetryRequest{
		AccountId: accountID, AppId: appID, DeploymentId: deploymentID,
		RouteTemplate: "GET /v1/usage", Method: "GET", HttpStatus: 503,
		LatencyMs: 20, ReceivedAtUnixMs: time.Date(2026, 9, 11, 12, 34, 10, 0, time.UTC).UnixMilli(),
		ConsumerId: consumerID, Count: 2, EventId: eventID,
	}
	first := r.handleOne(context.Background(), req)
	if first.GetOutcome() != rtOutcomeRateLimited {
		t.Fatalf("first outcome = %q, want %q for Free debug telemetry", first.GetOutcome(), rtOutcomeRateLimited)
	}
	if len(store.usage) != 1 || store.usage[0].RequestCount != 2 || store.usage[0].ErrorCount != 2 || store.usage[0].ConsumerKey != consumerID {
		t.Fatalf("usage = %#v, want one consumer event with 2 requests and 2 errors", store.usage)
	}
	second := r.handleOne(context.Background(), req)
	if second.GetOutcome() != rtOutcomeRateLimited || len(store.usage) != 1 {
		t.Fatalf("retry outcome/usage = %q/%d, want rate_limited/1", second.GetOutcome(), len(store.usage))
	}
}
