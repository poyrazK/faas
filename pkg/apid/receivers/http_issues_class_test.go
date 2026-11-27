package receivers

import (
	"context"
	"testing"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
)

// A wake_failed error is a 5xx the app never answered; it must not open an
// HTTP issue (Observe audit 2026-11-21, finding 11).
func TestRecordHTTPIssueSkipsWakeFailed(t *testing.T) {
	outcome, err := recordHTTPIssue(context.Background(), nil, &apidpb.IncrementAppErrorRequest{
		HttpStatus: 503, DeploymentId: "dep-1", ErrorClass: "wake_failed",
	})
	if err != nil || outcome != httpIssueSkipped {
		t.Fatalf("outcome = %v, err = %v; want skipped", outcome, err)
	}
	if !issueEligibleErrorClass("unhandled") || issueEligibleErrorClass("wake_failed") {
		t.Fatal("issueEligibleErrorClass: unhandled must open issues, wake_failed must not")
	}
	r := &appErrorsReceiver{}
	if got := r.enqueueHTTPIssue(&apidpb.IncrementAppErrorRequest{HttpStatus: 503, DeploymentId: "d", RequestId: "r", ErrorClass: "wake_failed"}); got != "" {
		t.Fatalf("enqueueHTTPIssue handed off a wake_failed error: %q", got)
	}
}
