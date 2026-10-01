package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDispatchMirror_AdmissionFailuresAreNotGuestCrashes(t *testing.T) {
	limits, ok := api.LimitsFor(api.PlanPro)
	if !ok {
		t.Fatal("Pro limits are unavailable")
	}
	tests := []struct {
		name        string
		err         error
		capSlot     bool
		wantFailure state.MirrorAdmissionFailureReason
	}{
		{name: "context timeout", err: context.DeadlineExceeded, wantFailure: state.MirrorAdmissionFailureTimeout},
		{name: "grpc timeout", err: status.Error(codes.DeadlineExceeded, "context deadline exceeded"), wantFailure: state.MirrorAdmissionFailureTimeout},
		{name: "concurrency rejection", err: api.ErrPlanLimitConcurrencyAt(limits, 2, 2), wantFailure: state.MirrorAdmissionFailureRejected},
		{name: "scheduler error", err: errors.New("scheduler unavailable"), wantFailure: state.MirrorAdmissionFailureError},
		{name: "gateway cap", capSlot: true, wantFailure: state.MirrorAdmissionFailureRejected},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			backend := &mirrorFakeBackend{scheduleErr: tc.err}
			store := &mirrorResultStoreFake{results: make(chan state.MirrorInvocationResult, 1)}
			h := NewHandlerWith(backend, nil, slog.New(slog.NewJSONHandler(io.Discard, nil))).WithMirrorResultStore(store)
			rule := MirrorRuleRow{ID: "rule-1", AppID: "app-1", AccountID: "acct-1", Percent: 100, IncludeBody: true}
			if tc.capSlot {
				h.MirrorMaxConcurrentPerRule = 1
				if !h.tryAcquireMirrorSlot(rule.ID) {
					t.Fatal("failed to pre-acquire mirror slot")
				}
			}
			source := newMirrorSourceCapture()
			source.writeHeader(http.StatusOK)
			source.write([]byte(`{"ok":true}`))
			source.complete()
			req := httptest.NewRequest(http.MethodGet, "http://example.test/export", nil)

			h.dispatchMirror(context.Background(), "source-instance", nil, rule, req, nil, "request-1", source)
			got := <-store.results
			if got.AdmissionFailureReason != tc.wantFailure {
				t.Errorf("admission failure = %q, want %q", got.AdmissionFailureReason, tc.wantFailure)
			}
			if got.Crashed || !got.ComparisonIncomplete {
				t.Errorf("crashed=%v comparison_incomplete=%v, want false/true", got.Crashed, got.ComparisonIncomplete)
			}
			if got.StatusDiff || got.SchemaDiff || got.BodyDiff {
				t.Errorf("admission failure must not report response diffs: %+v", got)
			}
		})
	}
}
