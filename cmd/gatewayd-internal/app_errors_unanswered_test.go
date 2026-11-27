package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
)

// Observe audit 2026-11-21 (finding 11): a 5xx the app never began to answer
// (a failed wake, a platform refusal) was filed as the app's "unhandled"
// exception, opened an HTTP issue and fired new-error alerts. It now keeps
// its own wake_failed group; a 5xx the app answered is still unhandled.
func TestAppErrorsRecorderFilesUnanswered5xxAsWakeFailed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		unanswered bool
		want       string
	}{
		{"app answered", false, "unhandled"},
		{"app never answered", true, appErrorClassUnanswered},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newAppErrorsRecorder(appErrorsRecorderConfig{Enabled: true}, nil, nil, slog.Default())
			h := r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				api.PlatformIdentity{RequestID: "01995a4e-8f20-7d8c-b9e1-2e9d2bd1d4f0", AppID: "app-1", DeploymentID: "dep-1", TenantID: "acct-1"}.ApplyGuestHeaders(req.Header)
				if tc.unanswered {
					gateway.ObservedRouteCarrierFrom(req.Context()).MarkUnanswered5xx()
				}
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/checkout", nil))
			rows := r.drainBatch(10)
			if len(rows) != 1 {
				t.Fatalf("rows = %d, want 1", len(rows))
			}
			if rows[0].ErrorClass != tc.want {
				t.Fatalf("error class = %q, want %q", rows[0].ErrorClass, tc.want)
			}
		})
	}
}
