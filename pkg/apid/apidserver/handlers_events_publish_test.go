package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/events"
)

// adr: 180
func TestPublishEventPersistsCanonicalEnvelope(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	when := time.Date(2026, 9, 17, 10, 11, 12, 0, time.UTC)
	rec := e.Do(t, http.MethodPost, "/v1/events:publish", api.PublishEventRequest{
		ID:     "evt-invoice-paid",
		Source: "billing.service",
		Type:   "invoice.paid",
		Time:   &when,
		Data:   json.RawMessage(`{"amount":150}`),
	}, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("publish status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var response api.PublishEventResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != "evt-invoice-paid" || response.AccountID != (*e.Acct()).ID {
		t.Fatalf("response = %+v", response)
	}

	// ADR-20261030170000: the receipt is a published event's only copy.
	work, err := (*e.Store()).ClaimDuePublishedEvent(context.Background(), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("claim published event: %v", err)
	}
	var envelope events.Envelope
	if err := json.Unmarshal(work.Payload, &envelope); err != nil {
		t.Fatalf("decode stored envelope: %v", err)
	}
	if envelope.ID != response.ID || envelope.AccountID != (*e.Acct()).ID || envelope.Type != "invoice.paid" {
		t.Fatalf("stored envelope = %+v", envelope)
	}
	if string(envelope.Data) != `{"amount":150}` {
		t.Fatalf("stored data = %s", envelope.Data)
	}
}

// adr: 180
func TestPublishEventRejectsCrossAccountAndInvalidEnvelope(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	other := "00000000-0000-0000-0000-000000000002"
	rec := e.Do(t, http.MethodPost, "/v1/events:publish", api.PublishEventRequest{
		ID:        "evt-cross-account",
		Source:    "billing.service",
		Type:      "invoice.paid",
		AccountID: other,
		Data:      json.RawMessage(`{}`),
	}, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-account status = %d, body=%s", rec.Code, rec.Body.String())
	}

	rec = e.Do(t, http.MethodPost, "/v1/events:publish", api.PublishEventRequest{
		ID:     "evt-invalid",
		Source: "billing.service",
		Type:   "invoice.paid",
		Data:   json.RawMessage(`not-json`),
	}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid envelope status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestPublishEventIdentityRejectsChangedContent(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	request := api.PublishEventRequest{ID: "evt-identity", Source: "billing", Type: "paid", Data: json.RawMessage(`{"amount":1}`)}
	for attempt := 0; attempt < 2; attempt++ {
		rec := e.Do(t, http.MethodPost, "/v1/events:publish", request, nil)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("identical retry %d: %d %s", attempt, rec.Code, rec.Body.String())
		}
	}
	request.Data = json.RawMessage(`{"amount":2}`)
	rec := e.Do(t, http.MethodPost, "/v1/events:publish", request, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("changed content: %d %s", rec.Code, rec.Body.String())
	}
	request.Source = "other-billing"
	rec = e.Do(t, http.MethodPost, "/v1/events:publish", request, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("different source: %d %s", rec.Code, rec.Body.String())
	}
}
