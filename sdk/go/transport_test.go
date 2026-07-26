package faas

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recordingTripper is a stub http.RoundTripper that captures each
// call's request body and lets the test control the response
// sequence via a function. It also tracks total calls for
// retry-count assertions.
type recordingTripper struct {
	mu     sync.Mutex
	calls  int
	bodies []string
	// fn is invoked per attempt; attempt is 1-based. Returning an
	// error short-circuits the retry loop (the SDK surfaces the
	// error immediately, no further attempts).
	fn func(req *http.Request, attempt int) (*http.Response, error)
}

func (r *recordingTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.calls++
	attempt := r.calls
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			r.mu.Unlock()
			return nil, err
		}
		r.bodies = append(r.bodies, string(b))
	} else {
		r.bodies = append(r.bodies, "")
	}
	r.mu.Unlock()
	return r.fn(req, attempt)
}

// okResponse is a tiny helper: 200 OK with no body. Use for
// "happy path" stubs.
func okResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     http.Header{},
	}
}

// statusResponse returns a response with the given status and an
// empty body. Body is a no-op closer so retry's drain+close works
// without leaking.
func statusResponse(status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     http.Header{},
	}
}

// newGetRequest builds a minimal GET for tests. The URL is
// irrelevant — the stub doesn't reach the network.
func newGetRequest(t *testing.T) *http.Request {
	t.Helper()
	r, err := http.NewRequest(http.MethodGet, "http://example.com/", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	return r
}

// TestLoggingRoundTripper_RecordsRequest: when a slog logger is
// attached, the wrapper emits one "request" and one "response"
// debug line per attempt. Verified by capturing slog output to
// a buffer.
func TestLoggingRoundTripper_RecordsRequest(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	stub := &recordingTripper{
		fn: func(req *http.Request, attempt int) (*http.Response, error) {
			return okResponse(), nil
		},
	}
	rt := newLoggingRoundTripper(stub, log)

	resp, err := rt.RoundTrip(newGetRequest(t))
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_ = resp.Body.Close()
	if stub.calls != 1 {
		t.Errorf("calls: got %d, want 1", stub.calls)
	}
	out := buf.String()
	if !strings.Contains(out, "faas http request") {
		t.Errorf("expected 'faas http request' in log, got:\n%s", out)
	}
	if !strings.Contains(out, "faas http response") {
		t.Errorf("expected 'faas http response' in log, got:\n%s", out)
	}
	if !strings.Contains(out, "method=GET") {
		t.Errorf("expected method=GET in log, got:\n%s", out)
	}
	if !strings.Contains(out, "status=200") {
		t.Errorf("expected status=200 in log, got:\n%s", out)
	}
}

// TestLoggingRoundTripper_NilLoggerIsNoop: when the logger is
// nil, the constructor returns the inner Transport identity, so
// the wrapper is observably a no-op.
func TestLoggingRoundTripper_NilLoggerIsNoop(t *testing.T) {
	stub := &recordingTripper{
		fn: func(req *http.Request, attempt int) (*http.Response, error) {
			return okResponse(), nil
		},
	}
	rt := newLoggingRoundTripper(stub, nil)
	if rt != http.RoundTripper(stub) {
		t.Errorf("nil logger should return inner Transport identity, got wrapper")
	}
	resp, err := rt.RoundTrip(newGetRequest(t))
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_ = resp.Body.Close()
	if stub.calls != 1 {
		t.Errorf("calls: got %d, want 1", stub.calls)
	}
}

// TestRetryRoundTripper_RetriesOn5xx: 500, 500, 200 → 3 attempts,
// final response is 200.
func TestRetryRoundTripper_RetriesOn5xx(t *testing.T) {
	responses := []int{500, 500, 200}
	stub := &recordingTripper{
		fn: func(req *http.Request, attempt int) (*http.Response, error) {
			return statusResponse(responses[attempt-1]), nil
		},
	}
	rt := newRetryRoundTripper(stub, 3, time.Millisecond)

	resp, err := rt.RoundTrip(newGetRequest(t))
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
	if stub.calls != 3 {
		t.Errorf("calls: got %d, want 3", stub.calls)
	}
}

// TestRetryRoundTripper_RetriesOn429: 429 is the canonical
// "rate-limited, try again" code. Must retry.
func TestRetryRoundTripper_RetriesOn429(t *testing.T) {
	responses := []int{429, 200}
	stub := &recordingTripper{
		fn: func(req *http.Request, attempt int) (*http.Response, error) {
			return statusResponse(responses[attempt-1]), nil
		},
	}
	rt := newRetryRoundTripper(stub, 2, time.Millisecond)

	resp, err := rt.RoundTrip(newGetRequest(t))
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
	if stub.calls != 2 {
		t.Errorf("calls: got %d, want 2", stub.calls)
	}
}

// TestRetryRoundTripper_NoRetryOn4xx: 400 is a client error. The
// server won't change its mind on retry, so the wrapper must
// return the 400 immediately.
func TestRetryRoundTripper_NoRetryOn4xx(t *testing.T) {
	stub := &recordingTripper{
		fn: func(req *http.Request, attempt int) (*http.Response, error) {
			return statusResponse(400), nil
		},
	}
	rt := newRetryRoundTripper(stub, 3, time.Millisecond)

	resp, err := rt.RoundTrip(newGetRequest(t))
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("status: got %d, want 400", resp.StatusCode)
	}
	if stub.calls != 1 {
		t.Errorf("calls: got %d, want 1 (no retry on 4xx other than 429)", stub.calls)
	}
}

// TestRetryRoundTripper_MaxZeroIsNoop: max<=0 disables retry. The
// constructor returns the inner Transport identity, so calls=1
// for any response.
func TestRetryRoundTripper_MaxZeroIsNoop(t *testing.T) {
	stub := &recordingTripper{
		fn: func(req *http.Request, attempt int) (*http.Response, error) {
			return statusResponse(500), nil
		},
	}
	rt := newRetryRoundTripper(stub, 0, time.Millisecond)
	if rt != http.RoundTripper(stub) {
		t.Errorf("max=0 should return inner Transport identity, got wrapper")
	}
	resp, err := rt.RoundTrip(newGetRequest(t))
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()
	if stub.calls != 1 {
		t.Errorf("calls: got %d, want 1", stub.calls)
	}
}

// TestRetryRoundTripper_BodyRewind: when a POST retries, the
// body must be replayable. We use http.NewRequest which sets
// GetBody; the second attempt must read the same body.
func TestRetryRoundTripper_BodyRewind(t *testing.T) {
	responses := []int{500, 200}
	stub := &recordingTripper{
		fn: func(req *http.Request, attempt int) (*http.Response, error) {
			return statusResponse(responses[attempt-1]), nil
		},
	}
	rt := newRetryRoundTripper(stub, 2, time.Millisecond)

	req, err := http.NewRequest(http.MethodPost, "http://example.com/", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
	if stub.calls != 2 {
		t.Errorf("calls: got %d, want 2", stub.calls)
	}
	if len(stub.bodies) != 2 {
		t.Fatalf("bodies: got %d, want 2", len(stub.bodies))
	}
	for i, b := range stub.bodies {
		if b != "hello" {
			t.Errorf("body[%d]: got %q, want %q", i, b, "hello")
		}
	}
}

// TestRetryRoundTripper_ContextCancel: a cancelled request
// context aborts the retry loop with the context error.
func TestRetryRoundTripper_ContextCancel(t *testing.T) {
	var calls atomic.Int32
	stub := &recordingTripper{
		fn: func(req *http.Request, attempt int) (*http.Response, error) {
			calls.Add(1)
			return statusResponse(500), nil
		},
	}
	rt := newRetryRoundTripper(stub, 5, 100*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.com/", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	// Cancel after 10ms — first attempt completes (returns 500),
	// the retry sleep is interrupted.
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	_, err = rt.RoundTrip(req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error: got %v, want context.Canceled", err)
	}
	if got := calls.Load(); got < 1 || got > 2 {
		t.Errorf("calls: got %d, want 1-2 (first attempt + at most one interrupted retry)", got)
	}
}

// TestRetryRoundTripper_NetworkErrorSurfacesImmediately: a
// transport-level error (e.g. connection refused) is not retried
// — the SDK's caller owns the context for cancellation.
func TestRetryRoundTripper_NetworkErrorSurfacesImmediately(t *testing.T) {
	want := errors.New("connection refused")
	stub := &recordingTripper{
		fn: func(req *http.Request, attempt int) (*http.Response, error) {
			return nil, want
		},
	}
	rt := newRetryRoundTripper(stub, 3, time.Millisecond)

	_, err := rt.RoundTrip(newGetRequest(t))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "connection refused" {
		t.Errorf("error: got %v, want %v", err, want)
	}
	if stub.calls != 1 {
		t.Errorf("calls: got %d, want 1 (no retry on network error)", stub.calls)
	}
}

// TestLoggingAndRetryStack: the two wrappers compose. A
// request that retries once should log two "request" lines
// and two "response" lines.
func TestLoggingAndRetryStack(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	responses := []int{500, 200}
	stub := &recordingTripper{
		fn: func(req *http.Request, attempt int) (*http.Response, error) {
			return statusResponse(responses[attempt-1]), nil
		},
	}
	// retry (outermost) → logging (middle) → stub (innermost).
	// This matches the production install order in client.go:74-79
	// (idempotency is even further inside, but for unit-test scope
	// we skip it). The logging RT sees each attempt; the retry RT
	// decides whether to retry based on the immediate response.
	rt := newRetryRoundTripper(newLoggingRoundTripper(stub, log), 2, time.Millisecond)

	resp, err := rt.RoundTrip(newGetRequest(t))
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()
	if stub.calls != 2 {
		t.Errorf("calls: got %d, want 2", stub.calls)
	}
	out := buf.String()
	// Count occurrences of "faas http request" — should be 2.
	if got, want := strings.Count(out, "faas http request"), 2; got != want {
		t.Errorf("request log count: got %d, want %d\n%s", got, want, out)
	}
	if got, want := strings.Count(out, "faas http response"), 2; got != want {
		t.Errorf("response log count: got %d, want %d\n%s", got, want, out)
	}
}
