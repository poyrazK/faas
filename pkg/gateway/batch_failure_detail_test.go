package gateway

import (
	"strings"
	"testing"
)

// prod 2026-11-19: a queue dead letter said "max_attempts: function
// state=failed" for both a handler's 400 and a thrown error.
func TestBatchFailureDetailNamesStatusAndExcerpt(t *testing.T) {
	if got := batchFailureDetail("failed", 400, []byte(`{"error":"bad order"}`)); got != `function returned HTTP 400: {"error":"bad order"}` {
		t.Fatalf("detail = %q", got)
	}
	if got := batchFailureDetail("failed", 0, nil); got != "function state=failed" {
		t.Fatalf("no status, no body: %q", got)
	}
	long := strings.Repeat("é", 400)
	got := batchFailureDetail("failed", 500, []byte(long))
	if !strings.HasSuffix(got, "…") || len(got) > len("function returned HTTP 500: ")+batchFailureDetailMaxBytes+len("…") || !strings.HasPrefix(got, "function returned HTTP 500: é") {
		t.Fatalf("long excerpt = %d bytes %q", len(got), got[:40])
	}
	if strings.Contains(got, "�") {
		t.Fatal("excerpt cut a rune")
	}
}
