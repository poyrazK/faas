package api

// adr: 050

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestContextWithIdempotencyKey_OverridesAndClears(t *testing.T) {
	ctx := ContextWithIdempotencyKey(context.Background(), "  deploy-1  ")
	if got := IdempotencyKeyFromContext(ctx); got != "deploy-1" {
		t.Fatalf("key = %q, want deploy-1", got)
	}
	cleared := ContextWithIdempotencyKey(ctx, "")
	if got := IdempotencyKeyFromContext(cleared); got != "" {
		t.Fatalf("cleared key = %q, want empty", got)
	}
	if got := IdempotencyKeyFromContext(nil); got != "" {
		t.Fatalf("nil context key = %q, want empty", got)
	}
}

func TestClient_Do_UsesContextIdempotencyKey(t *testing.T) {
	const want = "deploy-context-key"
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Idempotency-Key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"app-1"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "token")
	ctx := ContextWithIdempotencyKey(context.Background(), want)
	if _, err := c.CreateApp(ctx, CreateAppRequest{Slug: "demo"}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if got != want {
		t.Fatalf("Idempotency-Key = %q, want %q", got, want)
	}
}

func TestApplyProjectPlan_UsesContextIdempotencyKey(t *testing.T) {
	const want = "project-apply-retry"
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Idempotency-Key")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ApplyResponse{ProjectID: "project-1"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "token")
	ctx := ContextWithIdempotencyKey(context.Background(), want)
	if _, err := c.ApplyProjectPlan(ctx, "plan-token", bytes.NewBufferString("source"), "source.tar.gz", "project", "main", 0, nil, nil, false); err != nil {
		t.Fatalf("ApplyProjectPlan: %v", err)
	}
	if got != want {
		t.Fatalf("Idempotency-Key = %q, want %q", got, want)
	}
}
