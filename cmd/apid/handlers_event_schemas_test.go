package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEventSchemaRegistrationAndPublishValidation(t *testing.T) {
	e := setup(t, api.PlanPro)
	definition := api.RegisterEventSchemaRequest{
		Source: "billing", Type: "invoice.paid", Version: "v1",
		Schema: json.RawMessage(`{"type":"object","required":["amount"],"properties":{"amount":{"type":"number"}}}`),
	}
	rec := e.do(t, http.MethodPost, "/v1/event-schemas", definition, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, http.MethodPost, "/v1/event-schemas", definition, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("repeat register: %d %s", rec.Code, rec.Body.String())
	}
	request := api.PublishEventRequest{ID: "invoice-1", Source: "billing", Type: "invoice.paid", Data: json.RawMessage(`{"amount":10}`)}
	rec = e.do(t, http.MethodPost, "/v1/events:publish", request, nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing version: %d %s", rec.Code, rec.Body.String())
	}
	request.SchemaVersion = "v1"
	request.Data = json.RawMessage(`{"amount":"bad"}`)
	rec = e.do(t, http.MethodPost, "/v1/events:publish", request, nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid data: %d %s", rec.Code, rec.Body.String())
	}
	request.Data = json.RawMessage(`{"amount":10}`)
	rec = e.do(t, http.MethodPost, "/v1/events:publish", request, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("valid data: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, http.MethodGet, "/v1/event-schemas?source=billing&type=invoice.paid", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var listed []state.EventSchema
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Version != "v1" {
		t.Fatalf("listed schemas: %+v", listed)
	}
	definition.Schema = json.RawMessage(`{"type":"array"}`)
	rec = e.do(t, http.MethodPost, "/v1/event-schemas", definition, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("changed version: %d %s", rec.Code, rec.Body.String())
	}
}
