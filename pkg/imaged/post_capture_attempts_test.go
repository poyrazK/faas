package imaged

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestPostCaptureActualActivationAttempts(t *testing.T) {
	f := hostingReplayFixture(t)
	f.handler.WithOpsMetrics(wire.NewOpsMetrics("imaged"))
	now := time.Now().UTC()
	f.handler.hostingVerificationNow = func() time.Time { return now }
	unavailable := true
	f.handler.WithHostingSmoke(func(context.Context, state.App, state.Deployment) (apihostingreceipt.SmokeResult, error) {
		if unavailable {
			return apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeSkipped}, &apihostingreceipt.VerificationUnavailableError{Code: apihostingreceipt.SmokeErrorGatewayUnavailable, Cause: errors.New("gateway unavailable")}
		}
		return apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeVerified}, nil
	})
	if err := f.handler.HandleNotification(t.Context(), f.notification); apihostingreceipt.VerificationRecoveryCode(err) == "" {
		t.Fatalf("expected recoverable verification: %v", err)
	}
	f.assertReplayable(t)
	if err := f.handler.HandleNotification(t.Context(), f.notification); !errors.Is(err, state.ErrHostingVerificationDeferred) {
		t.Fatalf("early replay=%v", err)
	}
	metrics := f.handler.postCaptureMetrics
	if n := testutil.ToFloat64(metrics.attempts.WithLabelValues("snapshot", "verification", "deferred")); n != 2 {
		t.Fatalf("retry count=%v", n)
	}
	progress := hostingProgress(t, f)
	now = *progress.RetryNotBefore
	unavailable = false
	if err := f.handler.HandleNotification(t.Context(), f.notification); err != nil {
		t.Fatal(err)
	}
	if n := testutil.ToFloat64(metrics.attempts.WithLabelValues("snapshot", "finalize", "success")); n != 1 {
		t.Fatalf("activation count=%v", n)
	}
	if err := f.handler.HandleNotification(t.Context(), f.notification); err != nil {
		t.Fatal(err)
	}
	if n := testutil.ToFloat64(metrics.attempts.WithLabelValues("snapshot", "evidence", "success")); n != 1 {
		t.Fatalf("replay mistaken for activation: %v", n)
	}
	if err := f.handler.handleSnapshotWritten(t.Context(), snapshotWrittenPayload{}); err == nil {
		t.Fatal("invalid evidence accepted")
	}
	if n := testutil.ToFloat64(metrics.attempts.WithLabelValues("snapshot", "evidence", "error")); n != 1 {
		t.Fatalf("error count=%v", n)
	}
}
