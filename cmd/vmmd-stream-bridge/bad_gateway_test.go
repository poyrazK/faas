package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Prod hunt r30: a crashed app's 502 sent public clients the inner network
// addresses from the transport error.
func TestGuestBadGatewayHidesTransportError(t *testing.T) {
	w := httptest.NewRecorder()
	guestBadGateway(w, "guest h1 roundtrip", errors.New("read tcp 10.0.0.1:60204->10.0.0.2:3000: read: connection reset by peer"))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
	body := w.Body.String()
	for _, leak := range []string{"10.0.0.", "roundtrip", "tcp"} {
		if strings.Contains(body, leak) {
			t.Fatalf("body %q leaks %q", body, leak)
		}
	}
	if !strings.Contains(body, guestBadGatewayBody) {
		t.Fatalf("body = %q, want %q", body, guestBadGatewayBody)
	}
}
