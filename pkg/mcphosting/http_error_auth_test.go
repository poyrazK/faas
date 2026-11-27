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
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Request: req}
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
		{"keys refused", meta + `, error="invalid_token", error_description="signing keys refused"`, `{}`, true, "invalid_token", "its JWKS URL refused the request (HTTP 401 invalid_token: signing keys refused)", "--public-auth open"},
		{"code only in body", `Bearer realm="x"`, `{"error":"invalid_token"}`, true, "invalid_token", "rejected the token", "audience"},
		{"hostile description dropped", meta + `, error="invalid_token", error_description="a` + "\x1b[2J" + `"`, `{}`, true, "invalid_token", "(HTTP 401 invalid_token)", ""},
		{"no challenge, non-JSON", "", "<html>", true, "authentication_required", "HTTP 401 authentication_required", ""},
		{"gateway refusal", `Bearer realm="gregale", error="invalid_token"`, `{}`, true, "invalid_token", "Gregale's gateway refused", "protected at Gregale's edge"},
		{"lowercase-insensitive keys label", meta + `, error="invalid_token", error_description="Signing keys unavailable"`, `{}`, true, "invalid_token", "could not check the token", "JWKS"},
		{"token sent but not a Bearer credential", meta, `{"error":"authentication_required"}`, true, "authentication_required", "did not accept the Authorization header", "bare token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := authResponse(http.StatusUnauthorized, tc.challenge, tc.body, tc.token)
			defer func() { _ = resp.Body.Close() }()
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

// A body of unknown length (one that may never end) is not read.
func TestMCP401SkipsBodyOfUnknownLength(t *testing.T) {
	resp := authResponse(http.StatusUnauthorized, `Bearer realm="x"`, `{"error":"invalid_token"}`, true)
	_ = resp.Body.Close()
	resp.ContentLength = -1
	resp.Body = io.NopCloser(blockingReader{})
	defer func() { _ = resp.Body.Close() }()
	if p := problemOf(t, httpResponseError(resp)); p.Code != "authentication_required" {
		t.Fatalf("code = %q, want the fallback without reading the body", p.Code)
	}
}

type blockingReader struct{}

func (blockingReader) Read([]byte) (int, error) { select {} }

func TestMCP403InsufficientScopeNamesScope(t *testing.T) {
	resp := authResponse(http.StatusForbidden, `Bearer error="insufficient_scope", scope="read write"`, `{}`, true)
	defer func() { _ = resp.Body.Close() }()
	p := problemOf(t, httpResponseError(resp))
	if p.Code != "insufficient_scope" || !strings.Contains(p.Detail, "read write") {
		t.Fatalf("problem = %+v", p)
	}
}

func TestBearerChallengeParamsInChallengeList(t *testing.T) {
	for _, header := range []string{
		`Basic realm="a, b", Bearer error="invalid_token", error_description="x \"y\""`,
		`Basic realm="use bearer tokens", Bearer error="invalid_token", error_description="x \"y\""`,
		`Bearer error="invalid_token", error_description="x \"y\"", Basic realm="z"`,
	} {
		got := bearerChallengeParams([]string{header})
		if got["error"] != "invalid_token" || got["error_description"] != `x "y"` || got["realm"] != "" {
			t.Fatalf("%s: params = %#v", header, got)
		}
	}
}

// A fresh deploy candidate's first JWKS fetch can fail; the server answers
// 401 invalid_token with Retry-After. The doctor retries that a bounded number
// of times: a provider still unreachable a second later failed the release
// (prod 2026-11-22).
func TestDoctorRetriesSigningKeysUnavailableBounded(t *testing.T) {
	old := doctorSigningKeyRetryDelay
	doctorSigningKeyRetryDelay = time.Millisecond
	t.Cleanup(func() { doctorSigningKeyRetryDelay = old })
	for _, tc := range []struct {
		failures int32
		ok       bool
		calls    int32
	}{{1, true, 2}, {doctorSigningKeyRetries, true, doctorSigningKeyRetries + 1}, {10, false, doctorSigningKeyRetries + 1}} {
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
