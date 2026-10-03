// adr: 429
package main

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestInternalRequestEvidencePersistsExactTimeWithoutFinancialUsage(t *testing.T) {
	store := &consumerTelemetryStore{account: state.Account{Plan: api.PlanPro}}
	receiver := newRequestTelemetryReceiver(store, nil, nil, true)
	recorder := gateway.NewRequestTelemetryRecorder(gateway.RequestTelemetryConfig{Enabled: true}, nil)
	handler := &gateway.Handler{}
	handler.WithRequestTelemetryRecorder(recorder)
	at := time.Date(2026, 10, 2, 3, 1, 17, 123000000, time.UTC)
	accountID, appID, deploymentID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	request := httptest.NewRequest("GET", "/private?credential=do-not-persist", nil)
	request.Header.Set("Authorization", "Bearer do-not-persist")
	for i := range 2 {
		handler.RecordServiceRequest(request, gateway.ServiceRequestObservation{
			AccountID: accountID, ScenarioTestRunID: "authoritative-run", ReceivedAt: at.Add(time.Duration(i) * time.Millisecond), Status: 200,
			Target: gateway.Target{AppID: appID, DeploymentID: deploymentID, InstanceID: "selected-instance", NodeID: "selected-node"},
		})
	}
	rows := recorder.DrainBatch(10)
	if len(rows) != 2 {
		t.Fatalf("rows=%+v", rows)
	}
	for _, row := range rows {
		out := receiver.handleOne(context.Background(), &apidpb.IncrementRequestTelemetryRequest{
			EventId: row.EventID.String(), AccountId: row.AccountID.String(), AppId: row.AppID.String(), DeploymentId: row.DeploymentID.String(),
			RouteTemplate: row.Route, Method: row.Method, HttpStatus: int32(row.Status), Count: int32(row.Count),
			ReceivedAtUnixMs: row.ReceivedAt.UnixMilli(), InstanceId: row.InstanceID, NodeId: row.NodeID, UsageOutboxed: row.UsageOutboxed,
		})
		if out.GetOutcome() != rtOutcomeInserted {
			t.Fatalf("receiver=%+v", out)
		}
	}
	if len(store.usage) != 0 || len(store.inserted) != 2 {
		t.Fatalf("usage=%+v telemetry=%+v", store.usage, store.inserted)
	}
	for i, inserted := range store.inserted {
		if !inserted.ReceivedAt.Valid || !inserted.ReceivedAt.Time.Equal(at.Add(time.Duration(i)*time.Millisecond)) || inserted.InstanceID.String != "selected-instance" || inserted.ConsumerID.Valid {
			t.Fatalf("receiver lost exact/nonfinancial target evidence: %+v", inserted)
		}
	}
	if rows[0].EventID == rows[1].EventID {
		t.Fatal("two guest responses shared an event identity")
	}
}
