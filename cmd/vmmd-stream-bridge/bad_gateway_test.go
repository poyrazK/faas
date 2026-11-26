package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"syscall"
	"testing"
)

// Prod hunt r30: a crashed app's 502 sent public clients the inner network
// addresses from the transport error. Each body names only what a customer
// can act on (code review: a refused dial is a port or bind mistake).
func TestGuestBadGatewayHidesTransportError(t *testing.T) {
	refused := fmt.Errorf("dial tcp 10.0.0.2:3000: %w", syscall.ECONNREFUSED)
	for _, tc := range []struct {
		name string
		op   string
		err  error
		want string
	}{
		{"reset", "guest h1 roundtrip", errors.New("read tcp 10.0.0.1:60204->10.0.0.2:3000: read: connection reset by peer"), guestBadGatewayBody},
		{"refused", "dial guest", refused, guestNotListeningBody},
		{"refused on roundtrip", "guest h2c roundtrip", refused, guestNotListeningBody},
		{"build request", guestBuildRequestFailure, &url.Error{Op: "parse", URL: "http://10.0.0.2:3000/x?token=secret", Err: errors.New("bad")}, guestForwardFailedBody},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			guestBadGateway(w, httptest.NewRequest(http.MethodGet, "/x", nil), tc.op, tc.err)
			if w.Code != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502", w.Code)
			}
			body := w.Body.String()
			for _, leak := range []string{"10.0.0.", "roundtrip", "tcp", "token"} {
				if strings.Contains(body, leak) {
					t.Fatalf("body %q leaks %q", body, leak)
				}
			}
			if !strings.Contains(body, tc.want) {
				t.Fatalf("body = %q, want %q", body, tc.want)
			}
		})
	}
}
