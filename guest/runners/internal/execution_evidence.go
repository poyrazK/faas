package internal

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// GuestExecutionEvidence is the bounded execution result for one handler
// invocation. It deliberately contains no request body, headers, or customer
// error text; the gateway stores only these closed, low-cardinality values.
type GuestExecutionEvidence struct {
	Runtime    string
	DurationMS int
	Outcome    string
	ErrorClass string
}

const (
	GuestOutcomeOK           = "ok"
	GuestOutcomeHTTPError    = "http_error"
	GuestOutcomeHandlerError = "handler_error"
	GuestOutcomeTimeout      = "timeout"
	GuestOutcomeCanceled     = "canceled"
	GuestOutcomeMissing      = "missing"

	GuestErrorHTTP5xx      = "http_5xx"
	GuestErrorHandlerExec  = "handler_exec"
	GuestErrorHandlerProto = "handler_protocol"
	GuestErrorTimeout      = "timeout"
	GuestErrorCanceled     = "canceled"
)

// ObserveGuestExecution classifies a completed invocation and clamps the
// duration so a malformed or stuck runner cannot inject an unbounded value.
func ObserveGuestExecution(ctx context.Context, runtime string, started time.Time, status int, err error) GuestExecutionEvidence {
	duration := time.Since(started).Milliseconds()
	if duration < 0 {
		duration = 0
	}
	if duration > 24*60*60*1000 {
		duration = 24 * 60 * 60 * 1000
	}
	evidence := GuestExecutionEvidence{Runtime: runtime, DurationMS: int(duration), Outcome: GuestOutcomeOK}
	if err == nil {
		if status >= http.StatusInternalServerError && status <= 599 {
			evidence.Outcome = GuestOutcomeHTTPError
			evidence.ErrorClass = GuestErrorHTTP5xx
		}
		return evidence
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		evidence.Outcome = GuestOutcomeTimeout
		evidence.ErrorClass = GuestErrorTimeout
		return evidence
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		evidence.Outcome = GuestOutcomeCanceled
		evidence.ErrorClass = GuestErrorCanceled
		return evidence
	}
	evidence.Outcome = GuestOutcomeHandlerError
	if strings.Contains(err.Error(), "decode response") || strings.Contains(err.Error(), "encode envelope") {
		evidence.ErrorClass = GuestErrorHandlerProto
	} else {
		evidence.ErrorClass = GuestErrorHandlerExec
	}
	return evidence
}

// ApplyResponseHeaders writes the platform-owned evidence headers after
// customer headers, preventing a customer response from spoofing the signal.
func (e GuestExecutionEvidence) ApplyResponseHeaders(h http.Header) {
	if e.Runtime == "" {
		return
	}
	h.Set(api.GuestEvidenceDurationHeader, strconv.Itoa(e.DurationMS))
	h.Set(api.GuestEvidenceRuntimeHeader, e.Runtime)
	h.Set(api.GuestEvidenceOutcomeHeader, e.Outcome)
	// A customer response may have supplied a same-named marker in the
	// envelope. Clear it before applying the platform-owned value so a
	// successful invocation cannot inherit a stale error class.
	h.Del(api.GuestEvidenceErrorClassHeader)
	if e.ErrorClass != "" {
		h.Set(api.GuestEvidenceErrorClassHeader, e.ErrorClass)
	}
}
