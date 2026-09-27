package internal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func exitError(t *testing.T, script string) error {
	t.Helper()
	err := exec.Command("/bin/sh", "-c", script).Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("sh -c %q: want *exec.ExitError, got %v", script, err)
	}
	return fmt.Errorf("handler exec: %w (stderr=%s)", err, "Traceback: /app/handler.py")
}

// TestWriteHandlerFailureClassifiesCustomerCodeAsTerminal pins the runner
// half of the terminal handler-error contract: gatewayd-internal treats a
// 500 whose JSON body carries error=="handler_error" as a terminal
// application failure, and any other 5xx as retryable. A handler that
// crashed must produce the envelope, or the durable invocation queue runs
// the failing handler again.
func TestWriteHandlerFailureClassifiesCustomerCodeAsTerminal(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		terminal bool
		message  string
	}{
		{name: "handler exited non-zero", err: exitError(t, "exit 1"), terminal: true, message: "handler process failed"},
		{name: "go handler panicked", err: exitError(t, "exit 2"), terminal: true, message: "handler process failed"},
		{name: "unparseable handler output", err: fmt.Errorf("decode response: %w (stdout=%s)", errors.New("invalid character 'h'"), "hello"), terminal: true, message: "handler returned an invalid response"},
		{name: "handler killed by a signal", err: exitError(t, "kill -9 $$"), terminal: false},
		{name: "timeout", err: fmt.Errorf("handler timeout: %w", context.DeadlineExceeded), terminal: false},
		{name: "worker pipe broke", err: fmt.Errorf("workerpool: read response: %w", errors.New("EOF")), terminal: false},
		{name: "worker frame undecodable", err: fmt.Errorf("workerpool: decode response: %w", errors.New("bad")), terminal: false},
		{name: "interpreter missing", err: fmt.Errorf("handler exec: %w", exec.ErrNotFound), terminal: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req.Header.Set(api.InvocationIDHeader, "inv-123")
			rec := httptest.NewRecorder()
			WriteHandlerFailure(rec, req, tc.err)

			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", rec.Code)
			}
			var body struct {
				Error        string `json:"error"`
				Message      string `json:"message"`
				InvocationID string `json:"invocation_id"`
			}
			decoded := json.Unmarshal(rec.Body.Bytes(), &body) == nil
			isTerminal := decoded && body.Error == "handler_error"
			if isTerminal != tc.terminal {
				t.Fatalf("terminal = %v, want %v (body %q)", isTerminal, tc.terminal, rec.Body.String())
			}
			if !tc.terminal {
				return
			}
			if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
				t.Errorf("content-type = %q", got)
			}
			if body.Message != tc.message || body.InvocationID != "inv-123" {
				t.Errorf("body = %+v, want message %q and invocation_id inv-123", body, tc.message)
			}
		})
	}
}

// A request the caller abandoned is not the handler's fault even if the
// process exited non-zero on the way down.
func TestWriteHandlerFailureCancelledRequestStaysRetryable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	WriteHandlerFailure(rec, req, exitError(t, "exit 1"))
	if rec.Body.String() != "handler error\n" {
		t.Fatalf("body = %q, want the plain retryable 500", rec.Body.String())
	}
}
