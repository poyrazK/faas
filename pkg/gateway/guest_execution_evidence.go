package gateway

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
)

const maxGuestExecutionDurationMS = 24 * 60 * 60 * 1000

type guestExecutionEvidence struct {
	mu         sync.Mutex
	Runtime    string
	DurationMS int
	Outcome    string
	ErrorClass string
	seen       bool
}

type guestExecutionEvidenceSnapshot struct {
	Runtime    string
	DurationMS int
	Outcome    string
	ErrorClass string
}

type guestExecutionEvidenceContextKey struct{}

func withGuestExecutionEvidence(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), guestExecutionEvidenceContextKey{}, &guestExecutionEvidence{}))
}

func guestExecutionEvidenceFromContext(ctx context.Context) (guestExecutionEvidenceSnapshot, bool) {
	evidence, ok := ctx.Value(guestExecutionEvidenceContextKey{}).(*guestExecutionEvidence)
	if !ok || evidence == nil {
		return guestExecutionEvidenceSnapshot{}, false
	}
	evidence.mu.Lock()
	defer evidence.mu.Unlock()
	return guestExecutionEvidenceSnapshot{
		Runtime: evidence.Runtime, DurationMS: evidence.DurationMS,
		Outcome: evidence.Outcome, ErrorClass: evidence.ErrorClass,
	}, evidence.seen
}

// recordGuestExecutionEvidence consumes a platform-owned runner header. The
// values are closed and bounded before they enter telemetry, and the header
// is always removed from the customer-visible response when recognized.
func recordGuestExecutionEvidence(ctx context.Context, name, value string) bool {
	evidence, ok := ctx.Value(guestExecutionEvidenceContextKey{}).(*guestExecutionEvidence)
	if !ok || evidence == nil {
		return false
	}
	name = http.CanonicalHeaderKey(strings.TrimSpace(name))
	value = strings.TrimSpace(value)
	evidence.mu.Lock()
	defer evidence.mu.Unlock()
	switch name {
	case api.GuestEvidenceDurationHeader:
		duration, err := strconv.Atoi(value)
		if err != nil || duration < 0 || duration > maxGuestExecutionDurationMS {
			return true
		}
		evidence.DurationMS = duration
		evidence.seen = true
	case api.GuestEvidenceRuntimeHeader:
		if !validGuestRuntime(value) {
			return true
		}
		evidence.Runtime = value
		evidence.seen = true
	case api.GuestEvidenceOutcomeHeader:
		if !validGuestOutcome(value) {
			return true
		}
		evidence.Outcome = value
		evidence.seen = true
	case api.GuestEvidenceErrorClassHeader:
		if !validGuestErrorClass(value) {
			return true
		}
		evidence.ErrorClass = value
		evidence.seen = true
	default:
		return false
	}
	return true
}

func validGuestRuntime(value string) bool {
	switch value {
	case "node22", "node24", "python312", "python313", "go124":
		return true
	default:
		return false
	}
}

func validGuestOutcome(value string) bool {
	switch value {
	case "ok", "http_error", "handler_error", "timeout", "canceled", "missing":
		return true
	default:
		return false
	}
}

func validGuestErrorClass(value string) bool {
	switch value {
	case "", "http_5xx", "handler_exec", "handler_protocol", "timeout", "canceled":
		return true
	default:
		return false
	}
}

func forwardedResponseHeader(ctx context.Context, dst http.Header, name, value string) {
	if !recordGuestExecutionEvidence(ctx, name, value) && !isGuestEvidenceHeader(name) {
		dst.Add(name, value)
	}
}

func isGuestEvidenceHeader(name string) bool {
	switch http.CanonicalHeaderKey(strings.TrimSpace(name)) {
	case api.GuestEvidenceDurationHeader, api.GuestEvidenceRuntimeHeader,
		api.GuestEvidenceOutcomeHeader, api.GuestEvidenceErrorClassHeader:
		return true
	default:
		return false
	}
}

func stripGuestEvidenceResponseHeaders(resp *http.Response) {
	if resp == nil || resp.Header == nil {
		return
	}
	ctx := context.Background()
	if resp.Request != nil {
		ctx = resp.Request.Context()
	}
	for _, name := range []string{
		api.GuestEvidenceDurationHeader,
		api.GuestEvidenceRuntimeHeader,
		api.GuestEvidenceOutcomeHeader,
		api.GuestEvidenceErrorClassHeader,
	} {
		for _, value := range resp.Header.Values(name) {
			recordGuestExecutionEvidence(ctx, name, value)
		}
		resp.Header.Del(name)
	}
}
