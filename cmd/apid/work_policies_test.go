package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkPolicyAsyncInvokeAPI(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := mustSeedApp(t, e, "work-api")
	path := "/v1/apps/work-api/work-policies/document-index"
	policy := api.UpsertWorkPolicyRequest{MaxRunningPerKey: 1, MaxRunningPerFairnessKey: 2,
		PendingUpdates: "keep_latest", DebounceMS: 3000, ExpiresAfterMS: 600000}
	put := e.do(t, http.MethodPut, path, policy, nil)
	if put.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", put.Code, put.Body.String())
	}
	var configured api.WorkPolicyResponse
	if err := json.Unmarshal(put.Body.Bytes(), &configured); err != nil {
		t.Fatal(err)
	}
	if configured.Name != "document-index" || configured.Revision != 1 || configured.MaxRunningPerFairnessKey != 2 {
		t.Fatalf("policy = %+v", configured)
	}
	listed := e.do(t, http.MethodGet, "/v1/apps/work-api/work-policies", nil, nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("GET = %d %s", listed.Code, listed.Body.String())
	}
	var policies api.WorkPolicyListResponse
	if err := json.Unmarshal(listed.Body.Bytes(), &policies); err != nil {
		t.Fatal(err)
	}
	if len(policies.Policies) != 1 || policies.Policies[0].Name != configured.Name {
		t.Fatalf("list = %+v", policies)
	}
	invoke := func(key string) state.Invocation {
		t.Helper()
		response := e.do(t, http.MethodPost, "/v1/apps/work-api/invoke/async", api.InvokeRequest{
			Payload: json.RawMessage(`{"document_id":"d1"}`),
			Work: &api.InvokeWork{Policy: "document-index", Key: json.RawMessage(key),
				FairnessKey: json.RawMessage(`"tenant-1"`)},
		}, nil)
		if response.Code != http.StatusAccepted {
			t.Fatalf("invoke = %d %s", response.Code, response.Body.String())
		}
		var accepted api.AsyncInvokeResponse
		if err := json.Unmarshal(response.Body.Bytes(), &accepted); err != nil {
			t.Fatal(err)
		}
		row, err := e.store.InvocationByID(context.Background(), accepted.ID)
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	first, second, other := invoke(`"d1"`), invoke(`"d1"`), invoke(`"d2"`)
	first, _ = e.store.InvocationByID(context.Background(), first.ID)
	if first.State != state.InvocationSuperseded || second.State != state.InvocationPending || other.State != state.InvocationPending {
		t.Fatalf("states = %s, %s, %s", first.State, second.State, other.State)
	}
	if !bytes.Equal(first.WorkKeyDigest, second.WorkKeyDigest) || bytes.Equal(first.WorkKeyDigest, other.WorkKeyDigest) || second.WorkSequence != 2 || second.WorkPolicyRevision != 1 {
		t.Fatalf("lanes = %+v, %+v, %+v", first, second, other)
	}
	if !bytes.Equal(second.WorkFairnessDigest, other.WorkFairnessDigest) || second.WorkFairnessLimit != 2 {
		t.Fatalf("fairness groups = %+v, %+v", second, other)
	}
	if app != second.AppID {
		t.Fatalf("invocation app = %s, want %s", second.AppID, app)
	}
	cancel := e.do(t, http.MethodPost, path+"/cancel-pending", api.CancelPendingWorkRequest{Key: json.RawMessage(`"d2"`)}, nil)
	if cancel.Code != http.StatusOK {
		t.Fatalf("cancel = %d %s", cancel.Code, cancel.Body.String())
	}
	var cancelled api.CancelPendingWorkResponse
	if err := json.Unmarshal(cancel.Body.Bytes(), &cancelled); err != nil {
		t.Fatal(err)
	}
	if cancelled.ID == "" || cancelled.CancelledCount != 1 {
		t.Fatalf("cancel receipt = %+v", cancelled)
	}
	other, _ = e.store.InvocationByID(context.Background(), other.ID)
	if other.State != state.InvocationCancelled {
		t.Fatalf("cancelled state = %s", other.State)
	}
	delayed := e.do(t, http.MethodPost, "/v1/apps/work-api/delayed-tasks", api.DelayedTaskRequest{
		DelaySeconds: 30,
		Payload:      json.RawMessage(`{"document_id":"d3"}`),
		Work: &api.InvokeWork{Policy: "document-index", Key: json.RawMessage(`"d3"`),
			FairnessKey: json.RawMessage(`"tenant-2"`)},
	}, nil)
	if delayed.Code != http.StatusCreated {
		t.Fatalf("delayed = %d %s", delayed.Code, delayed.Body.String())
	}
	var task api.DelayedTaskResponse
	if err := json.Unmarshal(delayed.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	delayedRow, err := e.store.InvocationByID(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if delayedRow.WorkPolicyName != "document-index" || delayedRow.WorkPolicyRevision != 1 || delayedRow.WorkExpiresAt == nil || delayedRow.DueAt.Before(task.ScheduledAt) {
		t.Fatalf("delayed policy admission = %+v", delayedRow)
	}
	if bytes.Equal(delayedRow.WorkFairnessDigest, second.WorkFairnessDigest) {
		t.Fatal("delayed task should use its selected fairness group")
	}
	queue := e.do(t, http.MethodPost, "/v1/apps/work-api/queues/send", api.QueueSendRequest{
		Payload: json.RawMessage(`{"document_id":"d4"}`),
		Work: &api.InvokeWork{Policy: "document-index", Key: json.RawMessage(`"d4"`),
			FairnessKey: json.RawMessage(`"tenant-2"`)},
	}, nil)
	if queue.Code != http.StatusCreated {
		t.Fatalf("queue send = %d %s", queue.Code, queue.Body.String())
	}
	var queued api.QueueSendResponse
	if err := json.Unmarshal(queue.Body.Bytes(), &queued); err != nil {
		t.Fatal(err)
	}
	queuedRow, err := e.store.InvocationByID(context.Background(), queued.ID)
	if err != nil || queuedRow.Source != state.InvocationQueue || queuedRow.QueueName != "" || queuedRow.WorkPolicyName != "document-index" {
		t.Fatalf("queued policy row = %+v, err=%v", queuedRow, err)
	}
	inbox := e.do(t, http.MethodPost, "/v1/apps/work-api/inbox", api.SendAppMessageRequest{
		Type: "document.edited", Data: json.RawMessage(`{"document_id":"d4"}`),
		Work: &api.InvokeWork{Policy: "document-index", Key: json.RawMessage(`"d4"`)},
	}, nil)
	if inbox.Code != http.StatusAccepted {
		t.Fatalf("inbox send = %d %s", inbox.Code, inbox.Body.String())
	}
	var inboxReceipt api.SendAppMessageResponse
	if err := json.Unmarshal(inbox.Body.Bytes(), &inboxReceipt); err != nil {
		t.Fatal(err)
	}
	inboxRow, err := e.store.InvocationByID(context.Background(), inboxReceipt.ID)
	if err != nil || inboxRow.Source != state.InvocationQueue || inboxRow.WorkSequence != queuedRow.WorkSequence+1 {
		t.Fatalf("inbox policy row = %+v, err=%v", inboxRow, err)
	}
	queuedRow, _ = e.store.InvocationByID(context.Background(), queued.ID)
	if queuedRow.State != state.InvocationSuperseded || inboxRow.State != state.InvocationPending {
		t.Fatalf("queue replacement states = %s, %s", queuedRow.State, inboxRow.State)
	}
	for _, path := range []string{"/v1/apps/work-api/queues/send", "/v1/apps/work-api/inbox"} {
		var body any = api.QueueSendRequest{QueueName: "named", Work: &api.InvokeWork{Policy: "document-index", Key: json.RawMessage(`"d5"`)}}
		if path == "/v1/apps/work-api/inbox" {
			body = api.SendAppMessageRequest{Type: "document.edited", Data: json.RawMessage(`{}`), QueueName: "named",
				Work: &api.InvokeWork{Policy: "document-index", Key: json.RawMessage(`"d5"`)}}
		}
		assertProblem(t, e.do(t, http.MethodPost, path, body, nil), http.StatusBadRequest, api.CodeValidation)
	}
	if _, err := e.store.CreateQueueBinding(context.Background(), state.QueueBinding{
		AccountID: e.acct.ID, AppID: app, Name: "documents", QueueName: "documents",
		Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1,
	}); err != nil {
		t.Fatal(err)
	}
	bound := e.do(t, http.MethodPost, "/v1/apps/work-api/queues/send", api.QueueSendRequest{
		Work: &api.InvokeWork{Policy: "document-index", Key: json.RawMessage(`"d6"`)},
	}, nil)
	assertProblem(t, bound, http.StatusBadRequest, api.CodeValidation)
	bad := e.do(t, http.MethodPost, "/v1/apps/work-api/invoke/async", api.InvokeRequest{
		Work: &api.InvokeWork{Policy: "document-index", Key: json.RawMessage(`{"not":"scalar"}`)},
	}, nil)
	assertProblem(t, bad, http.StatusBadRequest, api.CodeValidation)
}

func TestWorkPolicyQueueReplacementAtDepthCap(t *testing.T) {
	e := setup(t, api.PlanHobby)
	appID := mustSeedApp(t, e, "queue-cap")
	policy := e.do(t, http.MethodPut, "/v1/apps/queue-cap/work-policies/latest", api.UpsertWorkPolicyRequest{
		MaxRunningPerKey: 1, PendingUpdates: "keep_latest", DebounceMS: 3000,
	}, nil)
	if policy.Code != http.StatusOK {
		t.Fatalf("policy = %d %s", policy.Code, policy.Body.String())
	}
	for i := 0; i < 4; i++ {
		response := e.do(t, http.MethodPost, "/v1/apps/queue-cap/queues/send", api.QueueSendRequest{}, nil)
		if response.Code != http.StatusCreated {
			t.Fatalf("fill queue %d = %d %s", i, response.Code, response.Body.String())
		}
	}
	send := func(key string) (int, string) {
		t.Helper()
		response := e.do(t, http.MethodPost, "/v1/apps/queue-cap/queues/send", api.QueueSendRequest{
			Work: &api.InvokeWork{Policy: "latest", Key: json.RawMessage(key)},
		}, nil)
		var receipt api.QueueSendResponse
		_ = json.Unmarshal(response.Body.Bytes(), &receipt)
		return response.Code, receipt.ID
	}
	if code, _ := send(`"doc-1"`); code != http.StatusCreated {
		t.Fatalf("first keyed send = %d", code)
	}
	if code, _ := send(`"doc-2"`); code != http.StatusForbidden {
		t.Fatalf("unrelated keyed send at cap = %d", code)
	}
	code, replacementID := send(`"doc-1"`)
	if code != http.StatusCreated || replacementID == "" {
		t.Fatalf("same-key replacement at cap = %d, id=%q", code, replacementID)
	}
	depth, err := e.store.CountPendingInvocations(context.Background(), appID, state.InvocationQueue)
	if err != nil || depth != api.MustLimitsFor(api.PlanHobby).MaxQueueDepth {
		t.Fatalf("queue depth after replacement = %d, err=%v", depth, err)
	}
}
