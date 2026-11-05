package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
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

func TestWorkflowEventRejectsUnsupportedNumericPayload(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "workflow-event-numeric")
	created := e.Do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/workflows/callback/runs", map[string]any{}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create run: %d %s", created.Code, created.Body)
	}
	var run api.WorkflowRunResponse
	if err := json.Unmarshal(created.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	request := api.InjectWorkflowEventRequest{EventName: "ready", Payload: json.RawMessage(`{"value":1e200000}`)}
	response := e.Do(t, http.MethodPost, "/v1/workflows/runs/"+run.ID+"/events", request, nil)
	apidserver.AssertProblem(t, response, http.StatusBadRequest, api.CodeValidation)
	events, err := (*e.Store()).GetWorkflowEventsForRun(t.Context(), run.ID)
	if err != nil || len(events) != 0 {
		t.Fatalf("invalid event stored: %v, %v", events, err)
	}
}

func TestWorkflowEventRejectsChangedRetainedKey(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "workflow-event-conflict")
	created := e.Do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/workflows/callback/runs", map[string]any{}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create run: %d %s", created.Code, created.Body)
	}
	var run api.WorkflowRunResponse
	if err := json.Unmarshal(created.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	key := "retained-key"
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(run.ID+"\x00"+key)).String()
	if err := (*e.Store()).InsertWorkflowEvent(t.Context(), &state.WorkflowEvent{ID: id, RunID: run.ID, EventName: "ready", Payload: json.RawMessage(`{"value":1}`)}); err != nil {
		t.Fatal(err)
	}
	response := e.Do(t, http.MethodPost, "/v1/workflows/runs/"+run.ID+"/events", api.InjectWorkflowEventRequest{EventName: "ready", Payload: json.RawMessage(`{"value":2}`)}, map[string]string{"Idempotency-Key": key})
	apidserver.AssertProblem(t, response, http.StatusConflict, api.CodeConflict)
}

func TestWorkflowCallbackRejectsUnsupportedNumericPayload(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "workflow-callback-numeric")
	created := e.Do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/workflows/callback/runs", map[string]any{}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create run: %d %s", created.Code, created.Body)
	}
	var run api.WorkflowRunResponse
	if err := json.Unmarshal(created.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	callbackID := api.WorkflowCallbackID(run.ID, "await")
	response := e.Do(t, http.MethodPost, "/v1/workflows/runs/"+run.ID+"/callbacks/"+callbackID, json.RawMessage(`{"value":1e200000}`), nil)
	apidserver.AssertProblem(t, response, http.StatusBadRequest, api.CodeValidation)
	events, err := (*e.Store()).GetWorkflowEventsForRun(t.Context(), run.ID)
	if err != nil || len(events) != 0 {
		t.Fatalf("invalid callback stored: %+v, %v", events, err)
	}
}

func TestWorkflowEventRetainedRetryAfterCompletion(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	app := seedWorkflowApp(t, e, "workflow-event-retained")
	created := e.Do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/workflows/callback/runs", map[string]any{}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create run: %d %s", created.Code, created.Body)
	}
	var run api.WorkflowRunResponse
	if err := json.Unmarshal(created.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	key := "retained-retry"
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(run.ID+"\x00"+key)).String()
	if err := (*e.Store()).InsertWorkflowEvent(t.Context(), &state.WorkflowEvent{ID: id, RunID: run.ID, EventName: "ready", Payload: json.RawMessage(`{"value":1}`)}); err != nil {
		t.Fatal(err)
	}
	if err := (*e.Store()).MarkWorkflowRunStatus(t.Context(), run.ID, state.WorkflowRunStatusSucceeded, nil, nil); err != nil {
		t.Fatal(err)
	}
	request := api.InjectWorkflowEventRequest{EventName: "ready", Payload: json.RawMessage(`{"value":1}`)}
	retry := e.Do(t, http.MethodPost, "/v1/workflows/runs/"+run.ID+"/events", request, map[string]string{"Idempotency-Key": key})
	if retry.Code != http.StatusOK {
		t.Fatalf("retained retry: %d %s", retry.Code, retry.Body)
	}
	fresh := e.Do(t, http.MethodPost, "/v1/workflows/runs/"+run.ID+"/events", request, map[string]string{"Idempotency-Key": "fresh"})
	apidserver.AssertProblem(t, fresh, http.StatusConflict, api.CodeWorkflowNotRunning)
}
