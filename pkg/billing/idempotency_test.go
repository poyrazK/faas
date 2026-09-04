package billing

import (
	"context"
	"testing"
)

func TestIdempotencyKeyContextRoundTrip(t *testing.T) {
	ctx := ContextWithIdempotencyKey(context.Background(), "operator-refund-1")
	if got, ok := IdempotencyKeyFromContext(ctx); !ok || got != "operator-refund-1" {
		t.Fatalf("IdempotencyKeyFromContext = %q, %t", got, ok)
	}
}

func TestIdempotencyKeyContextRejectsEmptyAndNil(t *testing.T) {
	for name, ctx := range map[string]context.Context{
		"nil":   nil,
		"empty": ContextWithIdempotencyKey(context.Background(), ""),
	} {
		t.Run(name, func(t *testing.T) {
			if got, ok := IdempotencyKeyFromContext(ctx); ok || got != "" {
				t.Fatalf("IdempotencyKeyFromContext = %q, %t; want empty, false", got, ok)
			}
		})
	}
}
