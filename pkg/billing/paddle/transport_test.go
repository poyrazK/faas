package paddle

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	paddle "github.com/PaddleHQ/paddle-go-sdk/v5"
)

// recordingRoundTripper captures the inbound request and returns a
// canned response. Mirrors the SDK's HTTPDoer signature: a single
// Do(req) returns the captured response. Tests assert on the
// captured request via the recorder's fields.
//
// The canned response is built lazily inside RoundTrip (rather than
// held as a struct field) so the bodyclose linter does not flag a
// literal `*http.Response` allocation in test setup. The
// RoundTripper under test never reads Body.
type recordingRoundTripper struct {
	captured *http.Request
	calls    int32
	header   http.Header
	err      error
}

func (r *recordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	atomic.AddInt32(&r.calls, 1)
	r.captured = req
	if r.err != nil {
		return nil, r.err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     r.header,
		Body:       nil,
		Request:    nil,
	}, nil
}

// okResponseHeader is the empty header set tests stamp on the
// canned response. The default http.Header{} works too, but pulling
// it into a named helper makes the test fixtures easier to scan and
// keeps the recordingRoundTripper struct smaller.
func okResponseHeader() http.Header { return http.Header{} }

// TestIdempotencyRoundTripper_InjectsHeaderOnPOSTTransactions — the
// load-bearing case. A POST to /transactions with X-Transit-Id
// stamped by the SDK (from paddle.ContextWithTransitID) gets
// Idempotency-Key copied to the same value. This is the durable
// forward-compat path the PR enables — once the Paddle SDK ships
// native Idempotency-Key support, the header value matches.
func TestIdempotencyRoundTripper_InjectsHeaderOnPOSTTransactions(t *testing.T) {
	t.Parallel()
	// okResponse sets Body to nil so the recordingRoundTripper can
	// return it without bodyclose flagging a leaked Body — we don't
	// read from the response in this test; assertions are on the
	// captured request.
	inner := &recordingRoundTripper{header: okResponseHeader()}
	rt := NewIdempotencyRT(inner)

	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, "https://api.paddle.com/transactions", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	// The SDK's internal/client/client.go:99 sets this header from
	// paddle.ContextWithTransitID. We set it directly here so the
	// test doesn't have to spin up a real SDK client.
	req.Header.Set(TransitIDHeader, "faas-overage-acct-abc-2026-07")

	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if got := inner.captured.Header.Get(IdempotencyKeyHeader); got != "faas-overage-acct-abc-2026-07" {
		t.Errorf("Idempotency-Key header = %q, want %q", got, "faas-overage-acct-abc-2026-07")
	}
	if got := inner.captured.Header.Get(TransitIDHeader); got != "faas-overage-acct-abc-2026-07" {
		t.Errorf("X-Transit-Id header lost in transit: got %q, want %q", got, "faas-overage-acct-abc-2026-07")
	}
}

// TestIdempotencyRoundTripper_SkipsNonTransactionsPaths — POSTs
// targeting other endpoints (product create, customer update) do
// not receive Idempotency-Key. meterd does not retry these calls
// from a transactional context; the retry budget is on the meterd
// side, not the merchant dashboard.
func TestIdempotencyRoundTripper_SkipsNonTransactionsPaths(t *testing.T) {
	t.Parallel()
	inner := &recordingRoundTripper{header: okResponseHeader()}
	rt := NewIdempotencyRT(inner)

	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, "https://api.paddle.com/products", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	req.Header.Set(TransitIDHeader, "some-id")

	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if got := inner.captured.Header.Get(IdempotencyKeyHeader); got != "" {
		t.Errorf("Idempotency-Key header set on /products POST: got %q, want empty", got)
	}
}

// TestIdempotencyRoundTripper_SkipsGETMethods — GETs must be
// idempotent at the protocol level; injecting Idempotency-Key on
// a GET is a documented anti-pattern. Even if the URL ends in
// /transactions, GETs pass through unmodified.
func TestIdempotencyRoundTripper_SkipsGETMethods(t *testing.T) {
	t.Parallel()
	inner := &recordingRoundTripper{header: okResponseHeader()}
	rt := NewIdempotencyRT(inner)

	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodGet, "https://api.paddle.com/transactions/txn_abc", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	req.Header.Set(TransitIDHeader, "some-id")

	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if got := inner.captured.Header.Get(IdempotencyKeyHeader); got != "" {
		t.Errorf("Idempotency-Key header set on GET /transactions: got %q, want empty", got)
	}
}

// TestIdempotencyRoundTripper_NoTransitID_NoHeader — if the SDK
// didn't stamp X-Transit-Id (because the caller didn't pass it
// via paddle.ContextWithTransitID), we don't inject an empty
// Idempotency-Key. An empty Idempotency-Key would either be
// silently ignored or, worse, treated as a unique key per call
// (depending on Paddle's server-side behaviour). Belt + braces.
func TestIdempotencyRoundTripper_NoTransitID_NoHeader(t *testing.T) {
	t.Parallel()
	inner := &recordingRoundTripper{header: okResponseHeader()}
	rt := NewIdempotencyRT(inner)

	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, "https://api.paddle.com/transactions", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	// Note: no X-Transit-Id set.

	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if got := inner.captured.Header.Get(IdempotencyKeyHeader); got != "" {
		t.Errorf("Idempotency-Key header set without X-Transit-Id: got %q, want empty", got)
	}
}

// TestIdempotencyRoundTripper_DelegatesError — the wrapper must
// surface inner transport errors unchanged. The pusher's classifier
// (ClassifyPushError) will collapse a net.OpError to the catch-all
// "other" label; the meterd pusher loop handles the retry.
func TestIdempotencyRoundTripper_DelegatesError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("dial tcp: connection refused")
	inner := &recordingRoundTripper{err: wantErr}
	rt := NewIdempotencyRT(inner)

	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, "https://api.paddle.com/transactions", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}

	resp, err := rt.RoundTrip(req)
	if err == nil {
		t.Fatalf("RoundTrip returned no error; want %v", wantErr)
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("RoundTrip err = %v, want errors.Is(_, %v) == true", err, wantErr)
	}
	if resp != nil {
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		t.Errorf("RoundTrip resp = %v, want nil on error", resp)
	}
}

// TestIdempotencyRoundTripper_NilInnerUsesDefault — defensive: if
// the caller passes nil for the inner transport, fall back to
// http.DefaultTransport instead of crashing on a nil-pointer
// deref in the next RoundTrip call. Matches the production
// pattern (sdk.WithClient(http.DefaultClient)).
func TestIdempotencyRoundTripper_NilInnerUsesDefault(t *testing.T) {
	t.Parallel()
	rt := NewIdempotencyRT(nil)
	if rt == nil {
		t.Fatalf("NewIdempotencyRT(nil) returned nil; want non-nil")
	}
	irt, ok := rt.(*idempotencyRoundTripper)
	if !ok {
		t.Fatalf("NewIdempotencyRT returned unexpected type %T", rt)
	}
	if irt.inner != http.DefaultTransport {
		t.Errorf("NewIdempotencyRT(nil) inner = %v, want http.DefaultTransport", irt.inner)
	}
}

// TestIdempotencyRoundTripper_WiredIntoPaddleSDK — sanity check
// that paddle.WithClient accepts an *http.Client with our wrapped
// transport. The SDK exposes WithClient(c client.HTTPDoer) where
// HTTPDoer = Do(req) (*http.Response, error) — *http.Client.Do
// matches that signature, so a *http.Client{Transport: rt} is
// drop-in. This test catches future SDK signature changes (e.g.,
// if Paddle swaps HTTPDoer for http.RoundTripper directly, the
// provider wiring in NewProvider must change too).
func TestIdempotencyRoundTripper_WiredIntoPaddleSDK(t *testing.T) {
	t.Parallel()
	rt := NewIdempotencyRT(http.DefaultTransport)
	client := &http.Client{Transport: rt}
	// paddle.WithClient's parameter type is client.HTTPDoer (an
	// interface satisfied by any type with a Do method matching
	// http.Client.Do). Asserting that the type satisfies the
	// interface at compile time would require importing
	// internal/client, which the Go module system blocks. The
	// next-best check: the paddle.New SDK constructor accepts
	// our wrapped client without panicking.
	sdk, err := paddle.New("test-key", paddle.WithClient(client))
	if err != nil {
		// paddle.New fails fast on programmer-error options (e.g.,
		// malformed base URL). A successful call here proves the
		// option shape is accepted by the SDK.
		t.Fatalf("paddle.New(WithClient) failed: %v", err)
	}
	if sdk == nil {
		t.Fatalf("paddle.New returned nil SDK; want non-nil")
	}
}

// TestIdempotencyRoundTripper_PassThroughPreservesOtherHeaders —
// the wrapper must not drop headers it doesn't know about. The SDK
// sets Authorization + User-Agent before the RoundTripper sees the
// request (internal/client/client.go:95-96), and downstream
// proxies may add X-Forwarded-For etc. Belt + braces.
func TestIdempotencyRoundTripper_PassThroughPreservesOtherHeaders(t *testing.T) {
	t.Parallel()
	inner := &recordingRoundTripper{header: okResponseHeader()}
	rt := NewIdempotencyRT(inner)

	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, "https://api.paddle.com/transactions", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	req.Header.Set(TransitIDHeader, "faas-overage-acct-abc-2026-07")
	req.Header.Set("Authorization", "Bearer pdl_test_xxx")
	req.Header.Set("X-Forwarded-For", "10.0.0.5")

	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if got := inner.captured.Header.Get("Authorization"); got != "Bearer pdl_test_xxx" {
		t.Errorf("Authorization header dropped: got %q", got)
	}
	if got := inner.captured.Header.Get("X-Forwarded-For"); got != "10.0.0.5" {
		t.Errorf("X-Forwarded-For header dropped: got %q", got)
	}
}

// TestShouldInjectIdempotencyKey_PathMatching — the path gate is
// tested directly so a future SDK that adds new transaction-shaped
// endpoints (e.g., /transactions/{id}/revise) gets coverage without
// needing a RoundTrip-level test per endpoint.
func TestShouldInjectIdempotencyKey_PathMatching(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{"POST /transactions", http.MethodPost, "/transactions", true},
		{"POST /transactions/nested", http.MethodPost, "/transactions/txn_abc/revise", true},
		{"GET /transactions", http.MethodGet, "/transactions/txn_abc", false},
		{"POST /products", http.MethodPost, "/products", false},
		{"POST /customers", http.MethodPost, "/customers/ctm_abc", false},
		{"PUT /transactions", http.MethodPut, "/transactions/txn_abc", false},
		{"empty path", http.MethodPost, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &http.Request{
				Method: tc.method,
				URL:    &url.URL{Path: tc.path},
			}
			if got := shouldInjectIdempotencyKey(req); got != tc.want {
				t.Errorf("shouldInjectIdempotencyKey(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
			}
		})
	}
}

// TestIdempotencyRoundTripper_RealHTTPServer_TransportOnly —
// end-to-end check that the wrapper is functionally equivalent to
// the inner transport on real network IO. Uses httptest.Server as
// a stand-in for api.paddle.com; the server-side handler asserts
// Idempotency-Key was set on POSTs that pass through our wrapper
// with the X-Transit-Id stamp. The test bypasses the Paddle SDK —
// it exercises only the RoundTripper's HTTP plumbing. A separate
// integration test (cmd/e2e) is the right place for SDK-level
// end-to-end coverage; this test's scope is the transport layer
// only. Note: paddle.ContextWithTransitID is the SDK's API for
// stamping X-Transit-Id, but the SDK only reads the context inside
// its own Client.Do wrapper — we don't go through the SDK here. We
// set X-Transit-Id directly to simulate what the SDK's wrapper
// would do, then assert our RoundTripper copies it as
// Idempotency-Key.
func TestIdempotencyRoundTripper_RealHTTPServer_TransportOnly(t *testing.T) {
	t.Parallel()
	var seenIDK string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/transactions") {
			seenIDK = r.Header.Get(IdempotencyKeyHeader)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rt := NewIdempotencyRT(http.DefaultTransport)
	client := &http.Client{Transport: rt}

	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, srv.URL+"/transactions", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	req.Header.Set(TransitIDHeader, "faas-overage-acct-real-2026-07")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if seenIDK != "faas-overage-acct-real-2026-07" {
		t.Errorf("server saw Idempotency-Key = %q, want %q", seenIDK, "faas-overage-acct-real-2026-07")
	}
}

// TestIdempotencyRoundTripper_DoesNotMutateCallerRequest —
// regression net for the contract that an http.RoundTripper MUST
// NOT mutate the caller's *http.Request. Per the net/http
// RoundTripper contract, the inbound request may be reused by the
// caller (retry middleware, test fixtures that re-issue the same
// request). An earlier version of this wrapper mutated req.Header
// in place; the fix-PR clones the request + Header before
// stamping Idempotency-Key. This test pins that contract.
func TestIdempotencyRoundTripper_DoesNotMutateCallerRequest(t *testing.T) {
	t.Parallel()
	inner := &recordingRoundTripper{header: okResponseHeader()}
	rt := NewIdempotencyRT(inner)

	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, "https://api.paddle.com/transactions", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	req.Header.Set(TransitIDHeader, "faas-overage-acct-abc-2026-07")

	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if got := req.Header.Get(IdempotencyKeyHeader); got != "" {
		t.Errorf("caller req.Header mutated: Idempotency-Key = %q, want empty (RoundTripper must clone)", got)
	}
	if got := req.Header.Get(TransitIDHeader); got != "faas-overage-acct-abc-2026-07" {
		t.Errorf("caller req.Header mutated: X-Transit-Id lost: got %q, want %q", got, "faas-overage-acct-abc-2026-07")
	}
	// Sanity: the inner transport still saw the cloned + stamped header.
	if got := inner.captured.Header.Get(IdempotencyKeyHeader); got != "faas-overage-acct-abc-2026-07" {
		t.Errorf("inner transport saw Idempotency-Key = %q, want %q", got, "faas-overage-acct-abc-2026-07")
	}
}
