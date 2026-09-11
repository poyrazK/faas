package main

import (
	"context"
	"testing"

	"github.com/google/uuid"
	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type consumerTelemetryStore struct {
	account  state.Account
	inserted []sqlc.InsertRequestTelemetryParams
}

func (s *consumerTelemetryStore) AccountByID(context.Context, string) (state.Account, error) {
	return s.account, nil
}

func (s *consumerTelemetryStore) InsertRequestTelemetry(_ context.Context, arg sqlc.InsertRequestTelemetryParams) error {
	s.inserted = append(s.inserted, arg)
	return nil
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
