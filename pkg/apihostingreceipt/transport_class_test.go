package apihostingreceipt

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"syscall"
	"testing"
)

// Prod 2026-11-22 (rc.367): the verifier origin had no DNS record and every
// candidate failed "transport is temporarily unavailable" with no reason.
func TestTransportUnavailableNamesItsClass(t *testing.T) {
	for _, tc := range []struct {
		cause error
		want  string
	}{
		{&url.Error{Op: "Get", URL: "https://gregale.app/healthz?token=secret", Err: &net.OpError{Op: "dial", Err: &net.DNSError{Name: "gregale.app", IsNotFound: true}}}, "(verifier origin gregale.app has no DNS record)"},
		{&url.Error{Op: "Get", URL: "https://x/?token=secret", Err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}}, "(connection refused)"},
		{fmt.Errorf("wrap: %w", context.DeadlineExceeded), "(timed out)"},
		{errors.New("something else secret"), "temporarily unavailable"},
	} {
		err := &VerificationUnavailableError{Code: SmokeErrorTransportUnavailable, Cause: tc.cause}
		if got := err.Error(); !strings.HasSuffix(got, tc.want) || strings.Contains(got, "secret") {
			t.Errorf("Error() = %q, want suffix %q and no cause text", got, tc.want)
		}
	}
}
