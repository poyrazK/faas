package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestRegionalReleaseAuthenticationBackendFailure(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
	}{
		{"backend", errors.New("backend unavailable private details"), http.StatusServiceUnavailable},
		{"missing", state.ErrNotFound, http.StatusUnauthorized},
		{"revoked", state.ErrAPIKeyRevoked, http.StatusUnauthorized},
		{"expired", state.ErrAPIKeyExpired, http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			regionalReleaseUnauthorized(rec, test.err)
			if rec.Code != test.status {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			if strings.Contains(rec.Body.String(), "private details") {
				t.Fatal("backend detail exposed")
			}
			if rec.Header().Get("WWW-Authenticate") != "" && test.status == http.StatusServiceUnavailable {
				t.Fatal("retryable failure invalidated bearer")
			}
		})
	}
}
