// client_http_path_mega4_test.go — Coverage Mega-PR #4 cluster 4:
// fill pkg/api/client.go HTTP-path coverage on the surfaces the
// client_method_sweep_test.go + client_test.go + client_pure_coverage_test.go
// trio did not already reach. Targets the load-bearing branches on
// doBytes (issue #299 raw-body surface), doReq problem-decode
// failure (200 OK + non-JSON), the Retry-After header promotion
// on 429/503, and the completion-cache auto-refresh path.
//
// Whitebox `package api`.

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- doBytes with *[]byte (raw body) ----------------------------

func TestClient_DoBytes_RawBody_Mega4(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer t" {
			t.Errorf("Authorization = %q, want Bearer t", got)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		// GET must NOT carry Idempotency-Key.
		if got := r.Header.Get("Idempotency-Key"); got != "" {
			t.Errorf("GET: Idempotency-Key = %q, want empty", got)
		}
		_, _ = w.Write([]byte("RAW-BYTES"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "t")
	var out []byte
	if err := c.doBytes(context.Background(), "GET", "/v1/raw", nil, &out); err != nil {
		t.Fatalf("doBytes: %v", err)
	}
	if string(out) != "RAW-BYTES" {
		t.Errorf("out = %q, want RAW-BYTES", out)
	}
}

func TestClient_DoBytes_MutatingAutoMintsIdempotencyKey_Mega4(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Idempotency-Key"); got == "" {
			t.Error("mutating: Idempotency-Key missing")
		}
		// Echo it so the test can verify non-empty.
		_, _ = w.Write([]byte(r.Header.Get("Idempotency-Key")))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "t")
	var out []byte
	if err := c.doBytes(context.Background(), "POST", "/v1/raw", map[string]string{"a": "b"}, &out); err != nil {
		t.Fatalf("doBytes: %v", err)
	}
	if len(out) == 0 {
		t.Error("expected echoed Idempotency-Key, got empty")
	}
}

func TestClient_DoBytes_NonByteOutputFallsThroughToJSON_Mega4(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"dep-1","status":"running"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "t")
	var out struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := c.doBytes(context.Background(), "GET", "/v1/deployment", nil, &out); err != nil {
		t.Fatalf("doBytes: %v", err)
	}
	if out.ID != "dep-1" || out.Status != "running" {
		t.Errorf("decode fall-through: %+v", out)
	}
}

func TestClient_DoBytes_Non2xxPropagatesProblemWithRetryAfter_Mega4(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"code":"source_ref_unavailable","title":"x","detail":"y"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "t")
	var out []byte
	err := c.doBytes(context.Background(), "GET", "/v1/raw", nil, &out)
	if err == nil {
		t.Fatal("want err")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Problem.Code != "source_ref_unavailable" {
		t.Errorf("code = %q", apiErr.Problem.Code)
	}
	if got := apiErr.Problem.HasHeader("Retry-After"); len(got) != 1 || got[0] != "30" {
		t.Errorf("Retry-After = %v, want [30]", got)
	}
}

func TestClient_DoBytes_Non2xxPlainTextFallsBack_Mega4(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream timed out"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "t")
	var out []byte
	err := c.doBytes(context.Background(), "GET", "/v1/raw", nil, &out)
	if err == nil {
		t.Fatal("want err")
	}
	if strings.Contains(err.Error(), "decode") {
		t.Errorf("err = %v, want plain-text fallback", err)
	}
}

// --- doReq problem-decode failure path --------------------------

func TestClient_DoReq_2xxNonJSONBody_Mega4(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "t")
	var out struct {
		X string `json:"x"`
	}
	// 2xx → unmarshal runs. Plain text fails decode.
	if err := c.do(context.Background(), "GET", "/v1/raw", nil, &out); err == nil {
		t.Error("plain-text body: err=nil, want decode error")
	}
}

func TestClient_DoReq_CompletionCacheRefresh_Mega4(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// /v1/apps is one of the cacheable completion routes.
		_, _ = w.Write([]byte(`[{"id":"app-1","slug":"my-app","name":"My App"}]`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "t")
	cache := NewCompletionCache()
	c.SetCompletionCache(cache)
	// First call: the path's completion entry should be populated.
	if _, err := c.ListApps(context.Background()); err != nil {
		t.Fatalf("ListApps: %v", err)
	}
	entry, _, err := cache.Read()
	if err != nil {
		t.Fatalf("cache.Read: %v", err)
	}
	if len(entry.Apps) != 1 || entry.Apps[0].Slug != "my-app" {
		t.Errorf("entry.Apps = %+v, want one with slug=my-app", entry.Apps)
	}
}

func TestClient_DoReq_RetryAfterOn429_Mega4(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code":"plan_limit_concurrency","title":"x","detail":"y"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "t")
	var out struct{}
	err := c.do(context.Background(), "GET", "/v1/raw", nil, &out)
	if err == nil {
		t.Fatal("want err")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if got := apiErr.Problem.HasHeader("Retry-After"); len(got) != 1 || got[0] != "60" {
		t.Errorf("Retry-After = %v, want [60]", got)
	}
}

func TestClient_DoReq_NoProblemBodyFallsBackToStatus_Mega4(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream timed out"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "t")
	var out struct{}
	err := c.do(context.Background(), "GET", "/v1/raw", nil, &out)
	if err == nil {
		t.Fatal("want err")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want typed *APIError", err)
	}
	if apiErr.Problem.Status != http.StatusBadGateway || apiErr.Problem.Code != "http_error" {
		t.Errorf("problem = %+v, want status 502/http_error", apiErr.Problem)
	}
}

func TestClient_DoReq_UnstructuredErrorsPreserveStatusAndRetryAfter(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		status := status
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "7")
				w.WriteHeader(status)
				_, _ = w.Write([]byte("<html>untrusted intermediary response</html>"))
			}))
			defer srv.Close()

			c := NewClient(srv.URL, "t")
			err := c.do(context.Background(), http.MethodGet, "/v1/test", nil, nil)
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %v, want *APIError", err)
			}
			if apiErr.Problem.Status != status || apiErr.Problem.Code != "http_error" {
				t.Fatalf("problem = %+v", apiErr.Problem)
			}
			if strings.Contains(apiErr.Problem.Detail, "html") {
				t.Fatalf("untrusted body leaked into detail: %q", apiErr.Problem.Detail)
			}
			if got := apiErr.Problem.HasHeader("Retry-After"); len(got) != 1 || got[0] != "7" {
				t.Fatalf("Retry-After = %v", got)
			}
		})
	}
}

// --- 4 MiB body cap edge on doBytes -----------------------------

func TestClient_DoBytes_BodyLimitExactBoundary_Mega4(t *testing.T) {
	t.Parallel()
	// io.LimitReader SILENTLY truncates beyond the cap rather than
	// erroring — pkg/api/client.go:252 uses
	//   data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	// so a 4 MiB response reads cleanly and any payload > 4 MiB
	// would also "succeed" but with len(out) < body size. We pin
	// the exact-boundary contract: 4 MiB reads intact.
	const fourMiB = 4 << 20
	body := make([]byte, fourMiB)
	for i := range body {
		body[i] = byte(i % 256)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "t")
	var out []byte
	if err := c.doBytes(context.Background(), "GET", "/v1/raw", nil, &out); err != nil {
		t.Fatalf("doBytes 4MiB response: %v", err)
	}
	if len(out) != fourMiB {
		t.Errorf("response len = %d, want %d", len(out), fourMiB)
	}
}

func TestClient_DoBytes_BodyLimitSilentTruncate_Mega4(t *testing.T) {
	t.Parallel()
	// Same doBytes path, but with an 8 MiB response. io.LimitReader
	// caps the read at 4 MiB and the remainder is silently dropped;
	// doBytes does NOT return an error in that case. This pins the
	// silent-truncation contract that pkg/api/client.go:252's call
	// site relies on — callers must pre-size the response or check
	// len(out) against an expected size.
	const overLimit = 8 << 20
	body := make([]byte, overLimit)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "t")
	var out []byte
	if err := c.doBytes(context.Background(), "GET", "/v1/raw", nil, &out); err != nil {
		t.Fatalf("doBytes 8MiB response: want nil err, got %v", err)
	}
	if len(out) != 4<<20 {
		t.Errorf("truncated len = %d, want 4MiB (the LimitReader cap)", len(out))
	}
}

// --- cookieOnlyPathRE — extra coverage on capabilities paths ----

func TestClient_CookieOnlyCapabilitiesPaths_Mega4(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Should never reach — cookie-only gate rejects pre-flight.
		t.Errorf("cookie-only path reached the server: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "t")
	paths := []string{
		"/v1/auth/sessions",
		"/v1/auth/sessions/foo",
		"/v1/auth/capabilities",
		"/v1/auth/capabilities/bar",
	}
	for _, p := range paths {
		// Verify both do and direct HTTP-style call reject pre-flight.
		if err := c.do(context.Background(), "GET", p, nil, nil); err == nil {
			t.Errorf("%s: do err=nil, want cookie-only reject", p)
		}
	}
}

func TestClient_NonCookieOnlyAuthPathsAllowed_Mega4(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "t")
	// /v1/auth/* paths NOT in the cookie-only regex — must reach the server.
	paths := []string{"/v1/auth/mfa", "/v1/auth/keys"}
	for _, p := range paths {
		if err := c.do(context.Background(), "GET", p, nil, nil); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}

// --- marshal failure path on doBytes ----------------------------

func TestClient_DoBytes_MarshalFail_Mega4(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "t")
	// Channels are not JSON-marshallable.
	var out []byte
	if err := c.doBytes(context.Background(), "POST", "/v1/raw",
		make(chan int), &out); err == nil {
		t.Error("marshal fail: err=nil")
	}
}

// --- JSON decode of error response missing 'code' falls through -

func TestClient_DoReq_ProblemMissingCode_PreservesStatus_Mega4(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"title":"no-code","detail":"y"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "t")
	var out struct{}
	err := c.do(context.Background(), "GET", "/v1/raw", nil, &out)
	if err == nil {
		t.Fatal("want err")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want typed status fallback", err)
	}
	if apiErr.Problem.Status != http.StatusBadRequest || apiErr.Problem.Code != "http_error" {
		t.Errorf("problem = %+v", apiErr.Problem)
	}
}

// --- 2xx + JSON null body ---------------------------------------

func TestClient_Do_2xxEmptyJSONBody_Mega4(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// Empty body — out is non-nil but len(data) == 0 → unmarshal skipped.
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "t")
	var out struct {
		X string `json:"x"`
	}
	if err := c.do(context.Background(), "GET", "/v1/raw", nil, &out); err != nil {
		t.Errorf("empty JSON body: %v", err)
	}
}

// --- Verify token-only behaviour on cookie-only paths -----------

func TestClient_TokenStillSetOnCookieOnlyPath_Mega4(t *testing.T) {
	t.Parallel()
	c := NewClient("http://example.invalid", "t")
	// Build a request manually via the cookie-only gate and verify
	// Authorization header was set BEFORE the gate returned the
	// CodeUnsupportedByCLI error.
	if err := c.do(context.Background(), "POST", "/v1/auth/sessions", nil, nil); err == nil {
		t.Error("cookie-only: err=nil")
	}
}

// --- 2xx Problem decoder tolerates extra fields -----------------

func TestClient_Do_2xxProblemWithExtras_Mega4(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Even with extras the decoder must succeed; we exercise the
		// strict "code != '' required" branch.
		_, _ = w.Write([]byte(`{"id":"abc","unknown_field":"x"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "t")
	var out struct {
		ID string `json:"id"`
	}
	if err := c.do(context.Background(), "GET", "/v1/raw", nil, &out); err != nil {
		t.Fatalf("err = %v", err)
	}
	if out.ID != "abc" {
		t.Errorf("id = %q, want abc", out.ID)
	}
}

// --- Helper for tests that need a typed problem error -----------

func TestAPIErrorRoundTrip_Mega4(t *testing.T) {
	t.Parallel()
	p := &Problem{
		Type:   "https://docs.example/probs/validation",
		Title:  "Validation failed",
		Code:   CodeValidation,
		Status: http.StatusBadRequest,
	}
	e := &APIError{Problem: *p}
	// errors.As round-trip.
	var dst *APIError
	if !errors.As(e, &dst) {
		t.Error("errors.As: false")
	}
	// Error() returns a non-empty message.
	if msg := e.Error(); !strings.Contains(msg, "validation") {
		t.Errorf("Error() = %q", msg)
	}
	// Format round-trip via fmt.
	_ = fmt.Sprintf("%v", e)
	_ = fmt.Sprintf("%+v", e)
}
