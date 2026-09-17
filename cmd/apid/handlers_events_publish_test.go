package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
)

// adr: 180
func TestPublishEventPersistsCanonicalEnvelope(t *testing.T) {
	e := setup(t, api.PlanHobby)
	when := time.Date(2026, 9, 17, 10, 11, 12, 0, time.UTC)
	rec := e.do(t, http.MethodPost, "/v1/events:publish", api.PublishEventRequest{
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
	if response.ID != "evt-invoice-paid" || response.AccountID != e.acct.ID {
		t.Fatalf("response = %+v", response)
	}

	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 20)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var found bool
	for _, row := range rows {
		if row.Kind != "event.published" {
			continue
		}
		found = true
		var envelope events.Envelope
		if err := json.Unmarshal(row.Data, &envelope); err != nil {
			t.Fatalf("decode stored envelope: %v", err)
		}
		if envelope.ID != response.ID || envelope.AccountID != e.acct.ID || envelope.Type != "invoice.paid" {
			t.Fatalf("stored envelope = %+v", envelope)
		}
		if string(envelope.Data) != `{"amount":150}` {
			t.Fatalf("stored data = %s", envelope.Data)
		}
	}
	if !found {
		t.Fatalf("event.published row not found in %+v", rows)
	}
}

// adr: 180
func TestPublishEventRejectsCrossAccountAndInvalidEnvelope(t *testing.T) {
	e := setup(t, api.PlanHobby)
	other := "00000000-0000-0000-0000-000000000002"
	rec := e.do(t, http.MethodPost, "/v1/events:publish", api.PublishEventRequest{
		ID:        "evt-cross-account",
		Source:    "billing.service",
		Type:      "invoice.paid",
		AccountID: other,
		Data:      json.RawMessage(`{}`),
	}, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-account status = %d, body=%s", rec.Code, rec.Body.String())
	}

	rec = e.do(t, http.MethodPost, "/v1/events:publish", api.PublishEventRequest{
		ID:     "evt-invalid",
		Source: "billing.service",
		Type:   "invoice.paid",
		Data:   json.RawMessage(`not-json`),
	}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid envelope status = %d, body=%s", rec.Code, rec.Body.String())
	}
}
