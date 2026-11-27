package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// prod 2026-11-19: a bucket finishing an inventory (503 + Retry-After 30)
// told the user to check service status and inspect a deploy.
func TestBusy503WithRetryAfterSaysWaitAndRetry(t *testing.T) {
	seconds := int64(30)
	busy := main.DiagnosticProblem(api.Problem{Status: 503, Code: "object_storage_bucket_transition", Detail: "The bucket is finishing a versioning or capacity inventory.", RetryAfterSeconds: &seconds})
	if !strings.Contains(busy.Hint, "Retry-After") || strings.Contains(busy.Hint, "deploy") {
		t.Fatalf("busy hint = %q", busy.Hint)
	}
	down := main.DiagnosticProblem(api.Problem{Status: 503, Code: "unavailable"})
	if !strings.Contains(down.Hint, "service status") {
		t.Fatalf("503 without Retry-After lost its outage hint: %q", down.Hint)
	}
}
