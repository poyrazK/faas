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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type consumerTelemetryStore struct {
	account  state.Account
	inserted []sqlc.InsertRequestTelemetryParams
	eventIDs []string
	usage    []state.APIConsumerUsageEvent
}

func TestConsumerUsageReceiptIndependentOfDebuggerAndIdempotent(t *testing.T) {
	store := &consumerTelemetryStore{account: state.Account{Plan: api.PlanFree}}
	receiver := newRequestTelemetryReceiver(store, nil, nil, false)
	event := &apidpb.ConsumerUsageEvent{
		EventId: uuid.NewString(), AccountId: uuid.NewString(), AppId: uuid.NewString(),
		ConsumerId: uuid.NewString(), PlatformTenantId: uuid.NewString(),
		WindowStartUnixMs: time.Date(2026, 9, 24, 12, 34, 0, 0, time.UTC).UnixMilli(),
		RequestCount:      1, BillableUnits: 1,
	}
	first, err := receiver.RecordConsumerUsage(context.Background(), event)
	if err != nil || !first.GetApplied() || !first.GetSurfaceAttributionSupported() {
		t.Fatalf("first receipt=%v err=%v", first, err)
	}
	second, err := receiver.RecordConsumerUsage(context.Background(), event)
	if err != nil || second.GetApplied() || !second.GetSurfaceAttributionSupported() {
		t.Fatalf("duplicate receipt=%v err=%v", second, err)
	}
	if len(store.usage) != 1 || store.usage[0].PlatformTenantID != event.GetPlatformTenantId() {
		t.Fatalf("usage=%+v", store.usage)
	}
}

// adr: 239
func TestConsumerUsageReceiptSupportsAnonymousTenantSurface(t *testing.T) {
	store := &consumerTelemetryStore{account: state.Account{Plan: api.PlanPro}}
	receiver := newRequestTelemetryReceiver(store, nil, nil, false)
	event := &apidpb.ConsumerUsageEvent{EventId: uuid.NewString(), AccountId: uuid.NewString(),
		AppId: uuid.NewString(), PlatformTenantId: uuid.NewString(), PlatformTenantSurfaceId: uuid.NewString(),
		WindowStartUnixMs: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC).UnixMilli(),
		RequestCount:      1, BillableUnits: 1}
	receipt, err := receiver.RecordConsumerUsage(context.Background(), event)
	if err != nil || !receipt.GetApplied() || !receipt.GetSurfaceAttributionSupported() || len(store.usage) != 1 ||
		store.usage[0].ConsumerKey != state.AnonymousConsumerKey || store.usage[0].PlatformTenantSurfaceID != event.PlatformTenantSurfaceId {
		t.Fatalf("surface receipt=%+v usage=%+v err=%v", receipt, store.usage, err)
	}
	event.ConsumerId = uuid.NewString()
	event.EventId = uuid.NewString()
	if _, err := receiver.RecordConsumerUsage(context.Background(), event); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("mixed consumer/surface event err=%v", err)
	}
}

func TestConsumerUsageRejectsMalformedEventWithoutAcknowledgement(t *testing.T) {
	store := &consumerTelemetryStore{}
	receiver := newRequestTelemetryReceiver(store, nil, nil, false)
	_, err := receiver.RecordConsumerUsage(context.Background(), &apidpb.ConsumerUsageEvent{EventId: "not-a-uuid"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code=%v err=%v", status.Code(err), err)
	}
	if len(store.usage) != 0 {
		t.Fatal("malformed event was recorded")
	}
}

func TestOutboxedDebuggerRowDoesNotWriteSecondUsageFact(t *testing.T) {
	store := &consumerTelemetryStore{account: state.Account{Plan: api.PlanPro}}
	receiver := newRequestTelemetryReceiver(store, nil, nil, true)
	req := &apidpb.IncrementRequestTelemetryRequest{
		EventId: uuid.NewString(), AccountId: uuid.NewString(), AppId: uuid.NewString(),
		DeploymentId: uuid.NewString(), RouteTemplate: "GET /v1/items", Method: "GET",
		HttpStatus: 200, LatencyMs: 10, ReceivedAtUnixMs: time.Now().UTC().UnixMilli(),
		Count: 2, UsageOutboxed: true,
	}
	out := receiver.handleOne(context.Background(), req)
	if out.GetOutcome() != rtOutcomeInserted {
		t.Fatalf("outcome=%q", out.GetOutcome())
	}
	if len(store.usage) != 0 {
		t.Fatalf("debugger wrote usage=%+v", store.usage)
	}
	if len(store.inserted) != 1 {
		t.Fatalf("debugger rows=%d", len(store.inserted))
	}
}

func (s *consumerTelemetryStore) AccountByID(context.Context, string) (state.Account, error) {
	return s.account, nil
}

func (s *consumerTelemetryStore) InsertRequestTelemetryWithLogEvent(_ context.Context, arg sqlc.InsertRequestTelemetryParams, eventID string) error {
	s.inserted = append(s.inserted, arg)
	s.eventIDs = append(s.eventIDs, eventID)
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
	if _, err := uuid.Parse(store.eventIDs[0]); err != nil {
		t.Fatalf("log event id %q is not a UUID: %v", store.eventIDs[0], err)
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

func TestRequestTelemetryReceiverPreservesPlatformTenantSnapshot(t *testing.T) {
	store := &consumerTelemetryStore{account: state.Account{Plan: api.PlanFree}}
	r := newRequestTelemetryReceiver(store, nil, nil, true)
	tenantID := uuid.NewString()
	req := &apidpb.IncrementRequestTelemetryRequest{
		AccountId: uuid.NewString(), AppId: uuid.NewString(), DeploymentId: uuid.NewString(),
		ConsumerId: uuid.NewString(), PlatformTenantId: tenantID, EventId: uuid.NewString(),
		RouteTemplate: "GET /", Method: "GET", HttpStatus: 200,
		LatencyMs: 1, ReceivedAtUnixMs: time.Now().UnixMilli(),
	}
	if got := r.handleOne(context.Background(), req).GetOutcome(); got != rtOutcomeRateLimited {
		t.Fatalf("outcome = %q, want rate_limited after durable usage", got)
	}
	if len(store.usage) != 1 || store.usage[0].PlatformTenantID != tenantID {
		t.Fatalf("usage lost immutable tenant snapshot: %+v", store.usage)
	}
	for _, tc := range []struct{ name, consumerID, tenantID string }{
		{"anonymous", "", uuid.NewString()},
		{"malformed", uuid.NewString(), "not-a-uuid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invalid := proto.Clone(req).(*apidpb.IncrementRequestTelemetryRequest)
			invalid.EventId = uuid.NewString()
			invalid.ConsumerId, invalid.PlatformTenantId = tc.consumerID, tc.tenantID
			if got := r.handleOne(context.Background(), invalid).GetOutcome(); got != rtOutcomeDBError || len(store.usage) != 1 {
				t.Fatalf("invalid attribution outcome=%q usage=%+v", got, store.usage)
			}
		})
	}
}

func TestRequestTelemetryLegacyEventIDSeparatesTenantTransition(t *testing.T) {
	store := &consumerTelemetryStore{account: state.Account{Plan: api.PlanFree}}
	r := newRequestTelemetryReceiver(store, nil, nil, true)
	req := &apidpb.IncrementRequestTelemetryRequest{
		AccountId: uuid.NewString(), AppId: uuid.NewString(), DeploymentId: uuid.NewString(),
		ConsumerId: uuid.NewString(), RouteTemplate: "GET /", Method: "GET", HttpStatus: 200,
		LatencyMs: 1, ReceivedAtUnixMs: time.Now().UnixMilli(),
	}
	_ = r.handleOne(context.Background(), req)
	linked := proto.Clone(req).(*apidpb.IncrementRequestTelemetryRequest)
	linked.PlatformTenantId = uuid.NewString()
	_ = r.handleOne(context.Background(), linked)
	if len(store.usage) != 2 || store.usage[0].EventID == store.usage[1].EventID ||
		store.usage[0].PlatformTenantID != "" || store.usage[1].PlatformTenantID != linked.PlatformTenantId {
		t.Fatalf("fallback event IDs conflated link transition: %+v", store.usage)
	}
}
