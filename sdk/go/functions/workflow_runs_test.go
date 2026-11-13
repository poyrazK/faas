package functions

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ADR-20261113120824 §3: Start posts to the platform API with the function's
// name; the run handle long-polls the run and signals it.
func TestStartWaitAndSignal(t *testing.T) {
	var started map[string]json.RawMessage
	var key, signalled string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/workflow-runs":
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &started)
			key = r.Header.Get("Idempotency-Key")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"run-1","status":"pending","done":false}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/workflow-runs/run-1/events":
			raw, _ := io.ReadAll(r.Body)
			signalled = string(raw)
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/workflow-runs/run-1":
			_, _ = w.Write([]byte(`{"id":"run-1","status":"succeeded","done":true,"output":{"shipped":true}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/workflow-runs/run-2":
			_, _ = w.Write([]byte(`{"id":"run-2","status":"failed","done":true,"error":"boom"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	env := envFor(map[string]string{FunctionsEnv: `{"orders":"shop-orders"}`, "GREGALE_PLATFORM_API_URL": srv.URL})
	ctx := context.Background()
	run, err := Start(ctx, "orders", map[string]int{"id": 1}, WithEnv(env), WithHTTPClient(srv.Client()), WithIdempotencyKey("order-1"))
	if err != nil || run.ID != "run-1" || string(started["function"]) != `"orders"` || string(started["input"]) != `{"id":1}` || key != "order-1" {
		t.Fatalf("start = %+v %v body=%v key=%q", run, err, started, key)
	}
	if err := run.Signal(ctx, "approved", map[string]bool{"ok": true}); err != nil || signalled != `{"event":"approved","payload":{"ok":true}}` {
		t.Fatalf("signal = %v body=%s", err, signalled)
	}
	var out struct {
		Shipped bool `json:"shipped"`
	}
	if err := run.Wait(ctx, &out); err != nil || !out.Shipped {
		t.Fatalf("wait = %+v %v", out, err)
	}
	failed := &Run{ID: "run-2", Function: "orders", platformURL: srv.URL, client: srv.Client()}
	var e *Error
	if err := failed.Wait(ctx, nil); !errors.As(err, &e) || e.Code != "workflow_run_failed" || e.Detail != "boom" {
		t.Fatalf("failed run = %v", err)
	}
	if _, err := Start[any](ctx, "nobody", nil, WithEnv(env)); err == nil {
		t.Fatal("an uncallable name started a run")
	}
}
