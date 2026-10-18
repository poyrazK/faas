package product

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// These narrow interfaces deliberately leave unexpected methods unimplemented.
type explanationStore struct {
	state.ProductControlStore
	state.OperationStore
	snapshot    state.ProductReceiptSnapshot
	readErr     error
	delivery    state.AppWebhookDelivery
	deliveryErr error
}

func (s explanationStore) GetProductReceipt(context.Context, string, string, string) (state.ProductReceiptSnapshot, error) {
	return s.snapshot, s.readErr
}

func (s explanationStore) OperationExecutions(context.Context, string, string, int, int) (api.OperationExecutionsResponse, error) {
	return api.OperationExecutionsResponse{Executions: []api.OperationExecution{{Generation: 1, State: "completed", Attempts: 1}}}, nil
}

func (s explanationStore) AppWebhookDeliveryByID(context.Context, string) (state.AppWebhookDelivery, error) {
	return s.delivery, s.deliveryErr
}

func TestProductExplanationRetainsCommercialEvidence(t *testing.T) {
	now := time.Now().UTC()
	s := explanationStore{snapshot: state.ProductReceiptSnapshot{ProductReceiptEvidence: api.ProductReceiptEvidence{OperationID: "operation", TenantID: "tenant", SucceededAt: &now, Receipt: api.ProductOperationReceipt{Revision: "original", Feature: "export", Plan: "starter", Currency: "EUR", UnitPriceMillicents: 25000}}}}
	view, err := explainOperation(t.Context(), s, "account", "app", "operation", 0)
	if err != nil || view.OperationRetained || view.Outcome != nil || view.Charge.State != "settled" || view.Charge.AmountMillicents != 25000 || view.Evidence.Receipt.Admission != nil {
		t.Fatalf("expired legacy receipt: %+v %v", view, err)
	}
	s.snapshot.SucceededAt = nil
	view, err = explainOperation(t.Context(), s, "account", "app", "operation", 0)
	if err != nil || view.Charge.State != "unsettled" || view.Charge.AmountMillicents != 0 {
		t.Fatalf("invented an expired outcome: %+v %v", view, err)
	}
	s.readErr = errors.New("storage unavailable")
	if _, err := explainOperation(t.Context(), s, "account", "app", "operation", 0); !errors.Is(err, s.readErr) {
		t.Fatalf("hid missing receipt evidence: %v", err)
	}
}

func TestProductExplanationRefreshesDeliveryWithoutChangingCharge(t *testing.T) {
	now := time.Now()
	s := explanationStore{snapshot: state.ProductReceiptSnapshot{ProductReceiptEvidence: api.ProductReceiptEvidence{SucceededAt: &now, Receipt: api.ProductOperationReceipt{UnitPriceMillicents: 25000}}, Operation: &state.Operation{AccountID: "account", AppID: "app", OperationResponse: api.OperationResponse{State: api.OperationSucceeded, CompletionDelivery: api.OperationDeliveryResponse{DeliveryID: "delivery", State: "pending"}}}}, delivery: state.AppWebhookDelivery{AccountID: "account", AppID: "app", Status: state.AppWebhookDeliverySucceeded}}
	view, err := explainOperation(t.Context(), s, "account", "app", "operation", 0)
	if err != nil || view.Outcome.CompletionDelivery != "succeeded" || view.Charge.State != "settled" || view.Charge.AmountMillicents != 25000 {
		t.Fatalf("stale delivery or repriced outcome: %+v %v", view, err)
	}
	s.deliveryErr = state.ErrNotFound
	view, err = explainOperation(t.Context(), s, "account", "app", "operation", 0)
	if err != nil || view.Outcome.CompletionDelivery != "delivery_expired" || view.Charge.State != "settled" {
		t.Fatalf("delivery retention lost charge: %+v %v", view, err)
	}
}

func TestProductExplanationCursor(t *testing.T) {
	for _, raw := range []string{"after=-1", "after=2147483648", "after=1&after=2", "after=", "tenant_id=foreign", "after=%zz"} {
		if _, err := explanationAfter(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}

func TestProductExplanationDistinguishesUnresolvedAndNonBillable(t *testing.T) {
	for _, tc := range []struct {
		state  api.OperationState
		charge string
	}{
		{api.OperationAccepted, "pending"}, {api.OperationRequiresReconciliation, "awaiting_reconciliation"},
		{api.OperationFailed, "not_billable"}, {api.OperationCancelled, "not_billable"},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			s := explanationStore{snapshot: state.ProductReceiptSnapshot{Operation: &state.Operation{OperationResponse: api.OperationResponse{State: tc.state}}}}
			view, err := explainOperation(t.Context(), s, "account", "app", "operation", 0)
			if err != nil || !view.OperationRetained || view.Charge.State != tc.charge || view.Charge.AmountMillicents != 0 {
				t.Fatalf("incorrect outcome charge: %+v %v", view, err)
			}
		})
	}
}
