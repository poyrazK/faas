// client_pure_coverage_test.go — fill pkg/api/client.go coverage gaps.
// Targets the constructor trio, Set/CompletionCache wiring, the
// cookie-only-path gate, the idempotency-key auto-mint, the base-URL
// accessors, the body-cap boundary, and the 4 MiB response truncation.
// Whitebox `package api`.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --- Constructors --------------------------------------------------

func TestNewClient_BaseFields(t *testing.T) {
	c := NewClient("https://api.example.com", "tok-1")
	if c.BaseURL() != "https://api.example.com" {
		t.Errorf("baseURL = %q", c.BaseURL())
	}
	if c.Token() != "tok-1" {
		t.Errorf("token = %q", c.Token())
	}
	if c.HTTPClient() == nil {
		t.Error("http client nil")
	}
	if c.HTTPClient().Timeout != 30*time.Second {
		t.Errorf("default timeout = %v", c.HTTPClient().Timeout)
	}
	if c.CompletionCache() == nil {
		t.Error("default CompletionCache nil; want auto-initialized")
	}
}

func TestNewClientWithDeployTimeout_PositiveSetsUploadClient(t *testing.T) {
	c := NewClientWithDeployTimeout("https://api.example.com", "tok", 5*time.Minute)
	if c.deployHTTP == nil {
		t.Fatal("deployHTTP nil")
	}
	if c.deployHTTP.Timeout != 5*time.Minute {
		t.Errorf("deploy timeout = %v", c.deployHTTP.Timeout)
	}
}

func TestNewClientWithDeployTimeout_NonPositiveFallsBack(t *testing.T) {
	// Zero duration → deployHTTP stays nil → uploadHTTP() returns the
	// default 30s HTTPClient.
	c := NewClientWithDeployTimeout("https://api.example.com", "tok", 0)
	if c.deployHTTP != nil {
		t.Error("zero timeout: deployHTTP set, want nil")
	}
	if c.uploadHTTP() != c.HTTPClient() {
		t.Errorf("uploadHTTP() != HTTPClient() with deployHTTP nil")
	}

	// Negative duration also falls back (the constructor guards on >0).
	c2 := NewClientWithDeployTimeout("https://api.example.com", "tok", -time.Second)
	if c2.deployHTTP != nil {
		t.Error("negative timeout: deployHTTP set, want nil")
	}
}

func TestClient_UploadHTTP_ReturnsDeployWhenSet(t *testing.T) {
	c := NewClientWithDeployTimeout("https://api.example.com", "tok", time.Minute)
	if c.uploadHTTP() != c.deployHTTP {
		t.Errorf("uploadHTTP() = %v, want deployHTTP", c.uploadHTTP())
	}
}

// --- SetCompletionCache / CompletionCache --------------------------

func TestClient_SetCompletionCache_ReturnsReceiver(t *testing.T) {
	c := NewClient("https://api.example.com", "tok")
	got := c.SetCompletionCache(nil)
	if got != c {
		t.Error("SetCompletionCache: not chainable")
	}
	if c.CompletionCache() != nil {
		t.Error("after Set(nil): CompletionCache not nil")
	}

	// Set a real cache; SetCompletionCache must replace, not merge.
	cache := NewCompletionCache()
	c.SetCompletionCache(cache)
	if c.CompletionCache() != cache {
		t.Error("after Set(cache): CompletionCache wrong")
	}
}

// --- cookieOnlyPathRE / do gate -----------------------------------

func TestClient_Do_CookieOnlyPathReturns403(t *testing.T) {
	c := NewClient("https://api.example.com", "tok")
	err := c.do(context.Background(), "GET", "/v1/auth/sessions", nil, nil)
	if err == nil {
		t.Fatal("cookie-only path: err = nil, want 403")
	}
	var p *Problem
	if !errors.As(err, &p) {
		t.Fatalf("not a *Problem: %T %v", err, err)
	}
	if p.Code != CodeUnsupportedByCLI {
		t.Errorf("code = %q, want %q", p.Code, CodeUnsupportedByCLI)
	}
	if p.Status != http.StatusForbidden {
		t.Errorf("status = %d, want 403", p.Status)
	}
	if p.DocsURL == "" {
		t.Error("DocsURL empty; cookie gate should point at the tripwire docs page")
	}
}

func TestClient_Do_CookieOnlyPathSubpathAlsoRejected(t *testing.T) {
	// /v1/auth/capabilities/foo must also match.
	c := NewClient("https://api.example.com", "tok")
	err := c.do(context.Background(), "GET", "/v1/auth/capabilities/foo", nil, nil)
	var p *Problem
	if !errors.As(err, &p) {
		t.Fatalf("not a *Problem: %T %v", err, err)
	}
	if p.Code != CodeUnsupportedByCLI {
		t.Errorf("code = %q", p.Code)
	}
}

func TestClient_Do_CookieOnlyPathBareSessionsRejected(t *testing.T) {
	// /v1/auth/sessions with NO trailing subpath must also match
	// (the regex's optional subpath group captures both).
	c := NewClient("https://api.example.com", "tok")
	err := c.do(context.Background(), "GET", "/v1/auth/sessions", nil, nil)
	var p *Problem
	if !errors.As(err, &p) {
		t.Fatalf("not a *Problem: %T %v", err, err)
	}
}

func TestClient_Do_NonCookiePathReachesServer(t *testing.T) {
	// A path that doesn't match cookieOnlyPathRE must reach the server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"method": r.Method,
			"path":   r.URL.Path,
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	var got map[string]string
	if err := c.do(context.Background(), "GET", "/v1/apps", nil, &got); err != nil {
		t.Fatalf("err = %v", err)
	}
	if got["path"] != "/v1/apps" {
		t.Errorf("server saw path %q", got["path"])
	}
	if got["method"] != "GET" {
		t.Errorf("server saw method %q", got["method"])
	}
}

// --- Idempotency-Key auto-mint ------------------------------------

func TestClient_Do_AutoMintsIdempotencyOnMutatingRequest(t *testing.T) {
	var seenKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenKey = r.Header.Get("Idempotency-Key")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "")
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		seenKey = ""
		if err := c.do(context.Background(), method, "/v1/x", nil, nil); err != nil {
			t.Fatalf("%s: err = %v", method, err)
		}
		if seenKey == "" {
			t.Errorf("%s: Idempotency-Key not auto-minted", method)
		}
	}
}

func TestClient_Do_GETDoesNotSetIdempotencyKey(t *testing.T) {
	var seenKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenKey = r.Header.Get("Idempotency-Key")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "")
	if err := c.do(context.Background(), "GET", "/v1/x", nil, nil); err != nil {
		t.Fatalf("err = %v", err)
	}
	if seenKey != "" {
		t.Errorf("GET: Idempotency-Key = %q, want empty", seenKey)
	}
}

func TestClient_Do_HEADDoesNotSetIdempotencyKey(t *testing.T) {
	var seenKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenKey = r.Header.Get("Idempotency-Key")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "")
	if err := c.do(context.Background(), "HEAD", "/v1/x", nil, nil); err != nil {
		t.Fatalf("err = %v", err)
	}
	if seenKey != "" {
		t.Errorf("HEAD: Idempotency-Key = %q, want empty", seenKey)
	}
}

func TestClient_Do_PreservesExplicitIdempotencyKey(t *testing.T) {
	// The auto-mint branch must not overwrite an explicitly-set
	// Idempotency-Key. We pre-set the header on a *http.Request,
	// then route through doReq (which is what every mutating SDK
	// helper that takes an explicit key—DeleteAccount, etc.—uses).
	const wantKey = "deadbeef-aaaa-bbbb-cccc-000000000000"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Idempotency-Key"); got != wantKey {
			t.Errorf("Idempotency-Key = %q, want %q", got, wantKey)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "tok")
	req, err := http.NewRequest("POST", srv.URL+"/v1/account", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Idempotency-Key", wantKey)
	if err := c.doReq(c.HTTPClient(), req, nil); err != nil {
		t.Fatalf("doReq: %v", err)
	}
}

// --- Body cap boundary -------------------------------------------

func TestClient_DoReq_BodyOver4MiBReturnsError(t *testing.T) {
	// The SDK caps response bodies at 4 MiB. Server returns >4 MiB;
	// the client must error rather than OOM.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		buf := make([]byte, 5<<20)
		for i := range buf {
			buf[i] = 'a'
		}
		_, _ = w.Write(buf)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "")
	// Decode into *string so the JSON unmarshal branch fires; the
	// 5 MiB payload can't be a valid JSON string so the cap-or-error
	// path triggers before the unmarshal.
	var out string
	if err := c.do(context.Background(), "GET", "/v1/x", nil, &out); err == nil {
		t.Error("oversize body: err = nil, want error")
	}
}

func TestClient_DoReq_BodyAt1MiBOk(t *testing.T) {
	// A 1 MiB payload must succeed (well under the 4 MiB cap). The
	// response body is a JSON string of about 1 MiB.
	big := strings.Repeat("a", (1<<20)-2) // leave room for JSON quotes
	body := `"` + big + `"`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "")
	var out string
	if err := c.do(context.Background(), "GET", "/v1/x", nil, &out); err != nil {
		t.Fatalf("at-cap body: err = %v", err)
	}
	if len(out) != (1<<20)-2 {
		t.Errorf("len = %d, want %d", len(out), (1<<20)-2)
	}
}

// --- Bearer + Content-Type auto-set --------------------------------

func TestClient_Do_SetsAuthorizationAndContentType(t *testing.T) {
	var sawAuth, sawCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawCT = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "the-token")
	body := map[string]string{"k": "v"}
	if err := c.do(context.Background(), "POST", "/v1/x", body, nil); err != nil {
		t.Fatalf("err = %v", err)
	}
	if sawAuth != "Bearer the-token" {
		t.Errorf("Authorization = %q", sawAuth)
	}
	if sawCT != "application/json" {
		t.Errorf("Content-Type = %q", sawCT)
	}
}

func TestClient_Do_NoTokenOmitsAuthorization(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("Authorization = %q, want empty", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "")
	if err := c.do(context.Background(), "GET", "/v1/x", nil, nil); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestClient_Do_GETDoesNotSetContentType(t *testing.T) {
	// GETs don't carry a body → no Content-Type: application/json.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Type"); got != "" {
			t.Errorf("Content-Type = %q, want empty on GET", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "tok")
	if err := c.do(context.Background(), "GET", "/v1/x", nil, nil); err != nil {
		t.Fatalf("err = %v", err)
	}
}

// --- Problem error on non-2xx + Retry-After surface --------------

func TestClient_Do_ServerErrorReturnsAPIErrorWithProblem(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(Problem{
			Code:   "test_code",
			Title:  "Test",
			Status: http.StatusForbidden,
			Detail: "test detail",
		})
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "")
	err := c.do(context.Background(), "GET", "/v1/x", nil, nil)
	if err == nil {
		t.Fatal("403: err = nil, want APIError")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %T, want *APIError", err)
	}
	if apiErr.Problem.Code != "test_code" {
		t.Errorf("Code = %q", apiErr.Problem.Code)
	}
	if apiErr.Problem.Status != http.StatusForbidden {
		t.Errorf("Status = %d", apiErr.Problem.Status)
	}
}

func TestClient_Do_RetryAfterHeaderSurfacesOn429(t *testing.T) {
	// The SDK stamps Retry-After from the wire response header into
	// the embedded Problem so callers can branch on HasHeader.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.Header().Set("Retry-After", "42")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(Problem{
			Code:   "throttled",
			Status: http.StatusTooManyRequests,
		})
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "")
	err := c.do(context.Background(), "GET", "/v1/x", nil, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %T, want *APIError", err)
	}
	if v := apiErr.Problem.HasHeader("Retry-After"); len(v) == 0 || v[0] != "42" {
		t.Errorf("HasHeader(Retry-After) = %v, want [42]", v)
	}
}

func TestClient_Do_RetryAfterHeaderSurfacesOn503(t *testing.T) {
	// Same Retry-After surface on 503 — Issue #739 / ADR-092 source-ref
	// unavailable backoff hint.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(Problem{
			Code:   "source_ref_unavailable",
			Status: http.StatusServiceUnavailable,
		})
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "")
	err := c.do(context.Background(), "GET", "/v1/x", nil, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %T, want *APIError", err)
	}
	if v := apiErr.Problem.HasHeader("Retry-After"); len(v) == 0 || v[0] != "7" {
		t.Errorf("HasHeader(Retry-After) = %v, want [7]", v)
	}
}

func TestClient_Do_ProblemDecodeFailurePreservesTypedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("kaboom"))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "")
	err := c.do(context.Background(), "GET", "/v1/x", nil, nil)
	if err == nil {
		t.Fatal("err = nil, want generic API error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("non-Problem body error = %T, want *APIError", err)
	}
	if apiErr.Problem.Status != http.StatusInternalServerError || apiErr.Problem.Code != "http_error" {
		t.Errorf("problem = %+v", apiErr.Problem)
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("err = %v, want 500 in chain", err)
	}
}

// --- doBytes: cyclic-boundary decode ------------------------------

func TestClient_DoBytes_BodyAt1MiBOk(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 1<<20)
		for i := range buf {
			buf[i] = 'c'
		}
		_, _ = w.Write(buf)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "")
	var out []byte
	if err := c.doBytes(context.Background(), "GET", "/v1/x", nil, &out); err != nil {
		t.Fatalf("doBytes: err = %v", err)
	}
	if len(out) != (1 << 20) {
		t.Errorf("len = %d, want %d", len(out), 1<<20)
	}
}

func TestClient_DoBytes_BodyOver4MiBTruncated(t *testing.T) {
	// doBytes uses the same LimitReader as do; oversize is truncated
	// at 4 MiB rather than erroring. (do decodes JSON so a truncated
	// payload errors on unmarshal; doBytes returns raw bytes verbatim.)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 5<<20)
		for i := range buf {
			buf[i] = 'd'
		}
		_, _ = w.Write(buf)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "")
	var out []byte
	if err := c.doBytes(context.Background(), "GET", "/v1/x", nil, &out); err != nil {
		t.Fatalf("doBytes: err = %v", err)
	}
	if len(out) != (4 << 20) {
		t.Errorf("len = %d, want 4 MiB cap", len(out))
	}
}
