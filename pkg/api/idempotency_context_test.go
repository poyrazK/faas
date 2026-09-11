package api

import (
	"context"
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
