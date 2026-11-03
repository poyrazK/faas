package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/events"
)

func TestAppEventRejectsOversizedWorkflowEnvelopeBeforeAcceptance(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	if r := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "large-event", RAMMB: 256}, nil); r.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", r.Code, r.Body)
	}
	data := json.RawMessage(`"` + strings.Repeat("x", int(api.AppEventPublishBodyMaxBytes)-150) + `"`)
	request := api.AppPublishEventRequest{Key: "large", Type: "order.created", Data: data}
	wire, err := json.Marshal(request)
	if err != nil || int64(len(wire)) > api.AppEventPublishBodyMaxBytes {
		t.Fatalf("invalid test body size %d: %v", len(wire), err)
	}
	r := e.Do(t, http.MethodPost, "/v1/apps/large-event/events:publish", request, nil)
	var problem api.Problem
	if r.Code != http.StatusRequestEntityTooLarge || json.Unmarshal(r.Body.Bytes(), &problem) != nil || problem.Code != "event_envelope_too_large" {
		t.Fatalf("large envelope: %d %s", r.Code, r.Body)
	}
	expanding := api.AppPublishEventRequest{Key: "expanding", Type: "order.created", Data: json.RawMessage(`[` + strings.Repeat(`1e10000,`, 105) + `1e10000]`)}
	r = e.Do(t, http.MethodPost, "/v1/apps/large-event/events:publish", expanding, nil)
	if r.Code != http.StatusRequestEntityTooLarge || json.Unmarshal(r.Body.Bytes(), &problem) != nil || problem.Code != "event_envelope_too_large" {
		t.Fatalf("expanding envelope: %d %s", r.Code, r.Body)
	}
}

func TestOversizedAcceptedAccountEventRetryReturnsOriginalReceipt(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	account := *e.Acct()
	data := json.RawMessage(`"` + strings.Repeat("x", int(api.WorkflowRunInputMaxBytes)-150) + `"`)
	request := api.PublishEventRequest{ID: "retained-large", Source: "orders", Type: "order.created", Data: data}
	envelope, err := (events.Envelope{ID: request.ID, Source: request.Source, Type: request.Type, Data: data}).Normalize(account.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(envelope)
	if err != nil || api.WorkflowEventEnvelopeFits(payload) {
		t.Fatalf("test event must exceed envelope limit: %v", err)
	}
	if err := (*e.Store()).AppendEvent(t.Context(), "apid", "event.published", &account.ID, payload); err != nil {
		t.Fatal(err)
	}
	r := e.Do(t, http.MethodPost, "/v1/events:publish", request, nil)
	if r.Code != http.StatusAccepted {
		t.Fatalf("single retry: %d %s", r.Code, r.Body)
	}
	r = e.Do(t, http.MethodPost, "/v1/events:publish-batch", map[string]any{"events": []api.PublishEventRequest{request}}, nil)
	var batch api.PublishEventBatchResponse
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &batch) != nil || len(batch.Results) != 1 || batch.Results[0].Status != "duplicate" || batch.Results[0].Receipt == nil {
		t.Fatalf("batch retry: %d %s", r.Code, r.Body)
	}
}
