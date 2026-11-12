package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Queue state's depth counts queue messages only; async invocations waiting
// for the drain showed nowhere (prod throughput test 2026-11-12). The
// response now carries the app's waiting work per source.
func TestQueueStateReportsBacklogBySource(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	acct := *e.Acct()
	ctx := t.Context()
	if r := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "backlog-app", RAMMB: 256}, nil); r.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", r.Code, r.Body)
	}
	app, err := store.AppBySlug(ctx, "backlog-app")
	if err != nil {
		t.Fatal(err)
	}
	var state0 api.QueueStateResponse
	if r := e.Do(t, http.MethodGet, "/v1/apps/backlog-app/queues/state", nil, nil); r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &state0) != nil || state0.BacklogBySource != nil {
		t.Fatalf("empty app queue state = %d %s", r.Code, r.Body)
	}
	for _, source := range []state.InvocationSource{state.InvocationAsyncInvoke, state.InvocationAsyncInvoke, state.InvocationInboundWebhook} {
		if _, err := store.EnqueueInvocation(ctx, state.Invocation{ID: uuid.NewString(), AppID: app.ID, AccountID: acct.ID,
			Source: source, State: state.InvocationPending, Method: http.MethodPost, Path: "/",
			Payload: json.RawMessage(`{}`), CreatedAt: time.Now(), DueAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	var got api.QueueStateResponse
	r := e.Do(t, http.MethodGet, "/v1/apps/backlog-app/queues/state", nil, nil)
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &got) != nil {
		t.Fatalf("queue state = %d %s", r.Code, r.Body)
	}
	if got.Depth != 0 || got.BacklogBySource[string(state.InvocationAsyncInvoke)] != 2 || got.BacklogBySource[string(state.InvocationInboundWebhook)] != 1 {
		t.Fatalf("queue state = %+v, want depth 0 and async_invoke=2 inbound_webhook=1 waiting", got)
	}
}
