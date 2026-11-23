package mcphosting

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func fastTokenRetry(t *testing.T) {
	t.Helper()
	saved := tokenRejectionRetryDelay
	tokenRejectionRetryDelay = time.Millisecond
	t.Cleanup(func() { tokenRejectionRetryDelay = saved })
}

func rejectWith(w http.ResponseWriter, challenge string) {
	w.Header().Set("WWW-Authenticate", challenge)
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
}

// A 401 to a request that carried a token used to read "supply a client token
// with --token-env" even though one was supplied; the real cause on prod was
// the server failing to fetch its JWKS (2026-11-23 rc.373 probe).
func TestRejectedTokenSaysSoAndNamesTheRFC6750Code(t *testing.T) {
	fastTokenRetry(t)
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		rejectWith(w, `Bearer resource_metadata="https://x/.well-known/oauth-protected-resource/mcp", scope="mcp:tools", error="invalid_token"`)
	})
	c.Token = "client-token"
	_, err := c.request(context.Background(), "tools/list", nil, nil, false)
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want APIError", err)
	}
	p := apiErr.Problem
	if p.Code != "mcp_token_rejected" || !strings.Contains(p.Detail, "invalid_token") || strings.Contains(p.Detail, "supply a client token") {
		t.Fatalf("problem = %+v", p)
	}
	if !strings.Contains(p.Hint, "JWKS") {
		t.Fatalf("hint = %q, want the signing-key cause named", p.Hint)
	}
}

func TestMissingTokenKeepsTheTokenEnvMessage(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		rejectWith(w, `Bearer resource_metadata="https://x/m", scope="mcp:tools"`)
	})
	_, err := c.request(context.Background(), "tools/list", nil, nil, false)
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) || apiErr.Problem.Code != "http_error" || !strings.Contains(apiErr.Problem.Detail, "--token-env") {
		t.Fatalf("error = %v", err)
	}
}

func TestChallengeErrorOnlyAcceptsRegisteredCodes(t *testing.T) {
	for challenge, want := range map[string]string{
		`Bearer error="invalid_token"`:                                       "invalid_token",
		`Bearer realm="apps", error=insufficient_scope`:                      "insufficient_scope",
		`bearer scope="a b", error="invalid_request", error_description="x"`: "invalid_request",
		`Bearer error="secret-marker"`:                                       "",
		`Basic error="invalid_token"`:                                        "",
		`Bearer realm="apps"`:                                                "",
	} {
		if got := bearerChallengeError([]string{challenge}); got != want {
			t.Errorf("%s: got %q, want %q", challenge, got, want)
		}
	}
}

func TestTransientTokenRejectionIsRetriedOnce(t *testing.T) {
	fastTokenRetry(t)
	var requests atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			rejectWith(w, `Bearer error="invalid_token"`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer client-token" {
			t.Error("retry dropped the token")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`))
	})
	c.Token = "client-token"
	if _, err := c.request(context.Background(), "tools/list", nil, nil, false); err != nil {
		t.Fatalf("request after one transient rejection: %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}
}

func TestPersistentRejectionAndOtherStatusesAreNotRetriedAgain(t *testing.T) {
	fastTokenRetry(t)
	for name, tc := range map[string]struct {
		token, challenge string
		status           int
		want             int32
	}{
		"persistent invalid_token": {"client-token", `Bearer error="invalid_token"`, 401, 2},
		"no token":                 {"", `Bearer error="invalid_token"`, 401, 1},
		"insufficient scope":       {"client-token", `Bearer error="insufficient_scope"`, 403, 1},
		"plain 401":                {"client-token", `Bearer realm="x"`, 401, 1},
	} {
		t.Run(name, func(t *testing.T) {
			var requests atomic.Int32
			c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("WWW-Authenticate", tc.challenge)
				w.WriteHeader(tc.status)
			})
			c.Token = tc.token
			if _, err := c.request(context.Background(), "tools/list", nil, nil, false); err == nil {
				t.Fatal("request succeeded")
			}
			if got := requests.Load(); got != tc.want {
				t.Fatalf("requests = %d, want %d", got, tc.want)
			}
		})
	}
}
