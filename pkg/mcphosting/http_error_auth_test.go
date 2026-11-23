package mcphosting

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func authResponse(status int, challenge, body string, token bool) *http.Response {
	req, _ := http.NewRequest(http.MethodPost, "https://mcp.example/mcp", nil)
	if token {
		req.Header.Set("Authorization", "Bearer x")
	}
	header := http.Header{"Content-Type": {"application/json"}}
	if challenge != "" {
		header.Set("WWW-Authenticate", challenge)
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: req}
}

func problemOf(t *testing.T, err error) api.Problem {
	t.Helper()
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error %T %v is not an APIError", err, err)
	}
	return apiErr.Problem
}

// A deploy's 401 said "authentication required" whatever the server said,
// which cost an hour on 2026-11-23: the server's error code must survive.
func TestMCP401KeepsServerErrorCode(t *testing.T) {
	const meta = `Bearer resource_metadata="https://mcp.example/.well-known/oauth-protected-resource/mcp", scope="read"`
	for _, tc := range []struct {
		name, challenge, body string
		token                 bool
		code, detail, hint    string
	}{
		{"no token", meta, `{"error":"authentication_required"}`, false, "authentication_required", "authentication required", "--token-env"},
		{"bad token", meta + `, error="invalid_token"`, `{"error":"invalid_token"}`, true, "invalid_token", "rejected the token (HTTP 401 invalid_token)", "audience"},
		{"keys unavailable", meta + `, error="invalid_token", error_description="signing keys unavailable"`, `{}`, true, "invalid_token", "could not check the token (HTTP 401 invalid_token: signing keys unavailable)", "JWKS"},
		{"code only in body", `Bearer realm="x"`, `{"error":"invalid_token"}`, true, "invalid_token", "rejected the token", "audience"},
		{"hostile description dropped", meta + `, error="invalid_token", error_description="a` + "\x1b[2J" + `"`, `{}`, true, "invalid_token", "(HTTP 401 invalid_token)", ""},
		{"no challenge, non-JSON", "", "<html>", true, "authentication_required", "HTTP 401 authentication_required", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := authResponse(http.StatusUnauthorized, tc.challenge, tc.body, tc.token)
			if !strings.HasPrefix(tc.body, "{") {
				resp.Header.Set("Content-Type", "text/html")
			}
			p := problemOf(t, httpResponseError(resp))
			if p.Code != tc.code || !strings.Contains(p.Detail, tc.detail) || !strings.Contains(p.Hint, tc.hint) {
				t.Fatalf("problem = code %q detail %q hint %q", p.Code, p.Detail, p.Hint)
			}
			if strings.ContainsRune(p.Detail, 0x1b) {
				t.Fatalf("detail carries a control byte: %q", p.Detail)
			}
		})
	}
}

func TestMCP403InsufficientScopeNamesScope(t *testing.T) {
	p := problemOf(t, httpResponseError(authResponse(http.StatusForbidden, `Bearer error="insufficient_scope", scope="read write"`, `{}`, true)))
	if p.Code != "insufficient_scope" || !strings.Contains(p.Detail, "read write") {
		t.Fatalf("problem = %+v", p)
	}
}

func TestBearerChallengeParamsInChallengeList(t *testing.T) {
	got := bearerChallengeParams([]string{`Basic realm="a, b", Bearer error="invalid_token", error_description="x \"y\""`})
	if got["error"] != "invalid_token" || got["error_description"] != `x "y"` {
		t.Fatalf("params = %#v", got)
	}
}

// A fresh deploy candidate's first JWKS fetch can fail; the server answers
// 401 invalid_token with Retry-After. The doctor retries that once only.
func TestDoctorRetriesSigningKeysUnavailableOnce(t *testing.T) {
	old := doctorGatewayRetryDelay
	doctorGatewayRetryDelay = time.Millisecond
	t.Cleanup(func() { doctorGatewayRetryDelay = old })
	for _, tc := range []struct {
		failures int32
		ok       bool
		calls    int32
	}{{1, true, 2}, {10, false, 2}} {
		inner, discovers := discoverOnlyHandler(t, 0, 0)
		var refused atomic.Int32
		h := func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Origin") == "" && refused.Load() < tc.failures && r.Header.Get("Mcp-Method") == "server/discover" {
				refused.Add(1)
				w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token", error_description="signing keys unavailable"`)
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			inner(w, r)
		}
		report := Doctor(context.Background(), testClient(t, h), false, "", nil)
		if report.OK != tc.ok || refused.Load()+discovers.Load() != tc.calls {
			t.Fatalf("failures=%d: ok=%v after %d refused + %d served: %+v", tc.failures, report.OK, refused.Load(), discovers.Load(), report.Checks)
		}
	}
}
