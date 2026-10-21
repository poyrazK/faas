package api

import (
	"context"
	"testing"
)

func TestProjectRetryContextCopiesTargets(t *testing.T) {
	targets := map[string]string{"app": "failed"}
	ctx := ContextWithProjectRetry(context.Background(), targets)
	targets["app"] = "changed"
	if got := projectRetryHeader(ctx); got != `{"app":"failed"}` {
		t.Fatalf("header=%s", got)
	}
	if got := projectRetryHeader(context.Background()); got != "" {
		t.Fatalf("unexpected header=%s", got)
	}
}
