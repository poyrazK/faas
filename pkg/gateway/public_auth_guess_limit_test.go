package gateway

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// A basic-auth gate checked every guess at full request rate with no failure
// limit and wrote one audit row per guess (bug hunt H76). Past the budget a
// client prefix gets 429 without its guess being checked, even a right one;
// another prefix is unaffected, and the flood writes one audit row.
func TestPublicAuth_BasicGuessesLimitedPerClientPrefix(t *testing.T) {
	t.Parallel()
	h, _, _, _, audit := newPublicAuthTestHandler(t, "basic", "acct-1")
	wrong := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:wrong"))
	right := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:s3cret"))
	from := func(auth, addr string) int {
		r := publicAuthReqFor(t, auth)
		r.Header.Set("X-Forwarded-For", addr)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, r)
		return rr.Code
	}
	for i := range api.PublicAuthBasicFailuresPerWindow {
		if code := from(wrong, fmt.Sprintf("203.0.113.%d", i+1)); code != http.StatusUnauthorized {
			t.Fatalf("guess %d: status %d, want 401", i, code)
		}
	}
	if code := from(right, "203.0.113.200"); code != http.StatusTooManyRequests {
		t.Fatalf("guess past the budget from the same /24: status %d, want 429", code)
	}
	if code := from(right, "198.51.100.7"); code != http.StatusOK {
		t.Fatalf("another client prefix: status %d, want 200", code)
	}
	if got := audit.counts["instances.public_auth_invalid"]; got != 1 {
		t.Fatalf("invalid audit rows = %d, want 1 per prefix per window", got)
	}
}
