package gateway

// adr: 207 — a matched public route is durably accepted before any app wake.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type recordingAsyncRouteEnqueuer struct {
	request AsyncRouteRequest
	calls   int
}

func (e *recordingAsyncRouteEnqueuer) EnqueueAsyncRoute(_ context.Context, request AsyncRouteRequest) (AsyncRouteAccepted, error) {
	e.request = request
	e.calls++
	return AsyncRouteAccepted{ID: "inv_async_123"}, nil
}

func TestPickFirstAsyncMatch(t *testing.T) {
	rules := []EdgeRuleAsyncResolved{
		{ID: "post-reports", Priority: 10, PathGlob: "/reports", Methods: map[string]bool{"POST": true}},
		{ID: "fallback", Priority: 20, PathGlob: "/*"},
	}
	if got := PickFirstAsyncMatch(rules, "/reports", http.MethodPost); got == nil || got.ID != "post-reports" {
		t.Fatalf("POST /reports match = %+v, want post-reports", got)
	}
	if got := PickFirstAsyncMatch(rules, "/reports", http.MethodGet); got == nil || got.ID != "fallback" {
		t.Fatalf("GET /reports match = %+v, want fallback", got)
	}
	if got := PickFirstAsyncMatch(rules[:1], "/other", http.MethodPost); got != nil {
		t.Fatalf("POST /other match = %+v, want nil", got)
	}
}

func TestApplyEdgeRuleAsyncEnqueuesAdmittedRequest(t *testing.T) {
	enqueuer := &recordingAsyncRouteEnqueuer{}
	h := &Handler{asyncRoutes: enqueuer}
	rule := &EdgeRuleAsyncResolved{ID: "rule_async", AccountID: "acct_1", AppID: "app_1"}
	app := App{
		ID:                        "app_1",
		AccountID:                 "acct_1",
		Plan:                      api.PlanHobby,
		RequestInvocationsEnabled: true,
	}
	r := httptest.NewRequest(http.MethodPost, "https://api.example.test/reports?format=pdf", strings.NewReader(`{"month":"2026-09"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", "report-september")
	r.Header.Set("Authorization", "Bearer secret")
	r.Header.Set("Cookie", "session=secret")
	r.Header.Set("Connection", "keep-alive, X-Remove-Me")
	r.Header.Set("X-Remove-Me", "connection-scoped")
	r.Header.Set("X-Faas-Internal", "secret")
	r.Header.Set("X-Request-Label", "finance")
	w := httptest.NewRecorder()

	if handled := h.applyEdgeRuleAsync(w, r, app, rule); !handled {
		t.Fatal("async rule was not handled")
	}
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", w.Code, w.Body.String())
	}
	if enqueuer.calls != 1 {
		t.Fatalf("enqueue calls = %d, want 1", enqueuer.calls)
	}
	got := enqueuer.request
	if got.AppID != app.ID || got.AccountID != app.AccountID || got.Method != http.MethodPost {
		t.Fatalf("request identity = %+v", got)
	}
	if got.Path != "/reports?format=pdf" {
		t.Errorf("path = %q, want path and query", got.Path)
	}
	if string(got.Payload) != `{"month":"2026-09"}` {
		t.Errorf("payload = %s", got.Payload)
	}
	if got.IdempotencyKey != "report-september" {
		t.Errorf("idempotency key = %q", got.IdempotencyKey)
	}
	for _, name := range []string{"Authorization", "Cookie", "Connection", "X-Faas-Internal", "X-Remove-Me"} {
		if _, ok := got.Headers[name]; ok {
			t.Errorf("sensitive header %s was persisted", name)
		}
	}
	if got.Headers["Content-Type"] != "application/json" || got.Headers["X-Request-Label"] != "finance" {
		t.Errorf("safe headers = %#v", got.Headers)
	}
	if w.Header().Get("Location") != "" {
		t.Errorf("Location = %q; relative control-plane URLs must not be advertised on the app host", w.Header().Get("Location"))
	}
	if w.Header().Get(api.InvocationIDHeader) != "inv_async_123" {
		t.Errorf("invocation header = %q", w.Header().Get(api.InvocationIDHeader))
	}
	var response api.AsyncInvokeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != "inv_async_123" || response.StatusURL != "/v1/invocations/inv_async_123" {
		t.Errorf("response = %+v", response)
	}
}

func TestApplyEdgeRuleAsyncRejectsInvalidJSON(t *testing.T) {
	enqueuer := &recordingAsyncRouteEnqueuer{}
	h := &Handler{asyncRoutes: enqueuer}
	r := httptest.NewRequest(http.MethodPost, "https://api.example.test/reports", strings.NewReader(`{"broken"`))
	w := httptest.NewRecorder()

	h.applyEdgeRuleAsync(w, r, App{
		ID: "app_1", AccountID: "acct_1", Plan: api.PlanHobby, RequestInvocationsEnabled: true,
	}, &EdgeRuleAsyncResolved{ID: "rule_async"})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", w.Code, w.Body.String())
	}
	if enqueuer.calls != 0 {
		t.Fatalf("enqueue calls = %d, want 0", enqueuer.calls)
	}
}
