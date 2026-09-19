package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAccountTraceLookupIncludesDurableQueueRow(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "trace-app")
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	inv, err := e.store.EnqueueInvocation(context.Background(), state.Invocation{
		AppID: appID, AccountID: e.acct.ID, Source: state.InvocationQueue,
		QueueName: "orders", Headers: json.RawMessage(`{"X-Gregale-Trace-Id":"4bf92f3577b34da6a3ce929d0e0e4736","traceparent":"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}`),
		DueAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation: %v", err)
	}

	rec := e.do(t, http.MethodGet, "/v1/account/traces/"+traceID, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var out api.AccountTraceLookupResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Invocations) != 1 || out.Invocations[0].ID != inv.ID {
		t.Fatalf("invocations = %+v, want %s", out.Invocations, inv.ID)
	}
	if out.Invocations[0].Traceparent == "" || !out.Partial {
		t.Fatalf("queue projection = %+v, partial=%v; MemStore telemetry should be enrichment-only", out.Invocations[0], out.Partial)
	}
}
