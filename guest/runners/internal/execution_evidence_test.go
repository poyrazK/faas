package internal

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestObserveGuestExecutionSuccess(t *testing.T) {
	e := ObserveGuestExecution(context.Background(), "node22", time.Now().Add(-12*time.Millisecond), http.StatusOK, nil)
	if e.Runtime != "node22" || e.Outcome != GuestOutcomeOK || e.DurationMS < 0 || e.ErrorClass != "" {
		t.Fatalf("unexpected evidence: %+v", e)
	}
}

func TestObserveGuestExecutionClassifiesErrors(t *testing.T) {
	tests := []struct {
		name, errText, wantOutcome, wantClass string
	}{
		{name: "protocol", errText: "decode response: invalid character", wantOutcome: GuestOutcomeHandlerError, wantClass: GuestErrorHandlerProto},
		{name: "exec", errText: "handler exec: exit status 1", wantOutcome: GuestOutcomeHandlerError, wantClass: GuestErrorHandlerExec},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := ObserveGuestExecution(context.Background(), "python312", time.Now(), 0, errors.New(tt.errText))
			if e.Outcome != tt.wantOutcome || e.ErrorClass != tt.wantClass {
				t.Fatalf("evidence = %+v", e)
			}
		})
	}
}

func TestObserveGuestExecutionTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	e := ObserveGuestExecution(ctx, "go124", time.Now(), 0, context.DeadlineExceeded)
	if e.Outcome != GuestOutcomeTimeout || e.ErrorClass != GuestErrorTimeout {
		t.Fatalf("evidence = %+v", e)
	}
}

func TestGuestExecutionEvidenceHeaders(t *testing.T) {
	h := http.Header{api.GuestEvidenceErrorClassHeader: []string{"handler_exec"}}
	GuestExecutionEvidence{Runtime: "node24", DurationMS: 7, Outcome: GuestOutcomeOK}.ApplyResponseHeaders(h)
	if got := h.Get(api.GuestEvidenceRuntimeHeader); got != "node24" || h.Get(api.GuestEvidenceDurationHeader) != "7" || h.Get(api.GuestEvidenceOutcomeHeader) != GuestOutcomeOK {
		t.Fatalf("headers = %v", h)
	}
	if strings.TrimSpace(h.Get(api.GuestEvidenceErrorClassHeader)) != "" {
		t.Fatalf("success should not have error class: %v", h)
	}
}
