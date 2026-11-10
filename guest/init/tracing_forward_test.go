package main

// adr: 20261108100000 — the in-VM trace bridge forwards a copy of every OTLP
// export to the customer's own endpoint, without delaying or failing the
// app's export, and without forwarding an SDK retry twice.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type forwardedRequest struct {
	path, contentType, contentEncoding, auth, tenant string
	body                                             string
}

type forwardSink struct {
	mu       sync.Mutex
	requests []forwardedRequest
	status   []int // per request; 200 once exhausted
	got      chan struct{}
}

func newForwardSink(status ...int) (*forwardSink, *httptest.Server) {
	sink := &forwardSink{status: status, got: make(chan struct{}, 64)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sink.mu.Lock()
		sink.requests = append(sink.requests, forwardedRequest{
			path: r.URL.Path, contentType: r.Header.Get("Content-Type"), contentEncoding: r.Header.Get("Content-Encoding"),
			auth: r.Header.Get("Authorization"), tenant: r.Header.Get("X-Scope-OrgID"), body: string(body),
		})
		code := http.StatusOK
		if len(sink.status) > 0 {
			code, sink.status = sink.status[0], sink.status[1:]
		}
		sink.mu.Unlock()
		w.WriteHeader(code)
		sink.got <- struct{}{}
	}))
	return sink, srv
}

func (s *forwardSink) wait(t *testing.T, n int) []forwardedRequest {
	t.Helper()
	for range n {
		select {
		case <-s.got:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for %d forwarded requests", n)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]forwardedRequest(nil), s.requests...)
}

func writeSecretsFile(t *testing.T, path string, secrets map[string]string) {
	t.Helper()
	data, err := json.Marshal(secrets)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func startForwardingBridge(t *testing.T, endpoint, secretsPath, secretName string, ack byte) (*traceBridge, *traceForwarder, *[]byte) {
	t.Helper()
	var mu sync.Mutex
	var lastFrame []byte
	bridge := newTraceBridge(func(_ context.Context, frame []byte) (byte, error) {
		mu.Lock()
		lastFrame = frame
		mu.Unlock()
		return ack, nil
	})
	source := &forwardHeaderSource{path: secretsPath, name: secretName}
	fwd := newTraceForwarder(api.TracingForward{Endpoint: endpoint, HeadersSecret: secretName}, source.get)
	fwd.sleep = func(context.Context, time.Duration) bool { return true }
	bridge.forward = fwd
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go fwd.run(ctx, 1)
	return bridge, fwd, &lastFrame
}

func TestTraceForwardCopiesTracesAndLogsWithSecretHeaders(t *testing.T) {
	sink, srv := newForwardSink()
	defer srv.Close()
	secrets := filepath.Join(t.TempDir(), "secrets.env")
	writeSecretsFile(t, secrets, map[string]string{"OTLP_HEADERS": "Authorization=Bearer%20tok,X-Scope-OrgID=team-a"})
	bridge, _, _ := startForwardingBridge(t, srv.URL+"/otlp/", secrets, "OTLP_HEADERS", api.TraceAckAccepted)

	if rec := postTraceExport(t, bridge, "application/x-protobuf", "gzip", []byte("trace-body")); rec.Code != http.StatusOK {
		t.Fatalf("trace export status = %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodPost, api.TraceLocalLogsPath, strings.NewReader(`{"resourceLogs":[]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	bridge.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("log export status = %d", rec.Code)
	}

	got := sink.wait(t, 2)
	byPath := map[string]forwardedRequest{}
	for _, r := range got {
		byPath[r.path] = r
	}
	traces, logs := byPath["/otlp/v1/traces"], byPath["/otlp/v1/logs"]
	if traces.body != "trace-body" || traces.contentType != "application/x-protobuf" || traces.contentEncoding != "gzip" {
		t.Fatalf("forwarded trace export = %+v", traces)
	}
	if logs.body != `{"resourceLogs":[]}` || logs.contentType != "application/json" || logs.contentEncoding != "" {
		t.Fatalf("forwarded log export = %+v", logs)
	}
	for _, r := range got {
		if r.auth != "Bearer tok" || r.tenant != "team-a" {
			t.Fatalf("headers from the secret not applied: %+v", r)
		}
	}
}

func TestTraceForwardDoesNotDependOnGregaleAck(t *testing.T) {
	sink, srv := newForwardSink()
	defer srv.Close()
	// Gregale refuses the export permanently; the copy still goes out.
	bridge, _, _ := startForwardingBridge(t, srv.URL, filepath.Join(t.TempDir(), "absent"), "", api.TraceAckRejected)
	if rec := postTraceExport(t, bridge, "application/x-protobuf", "", []byte("body")); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want the host's refusal unchanged", rec.Code)
	}
	if got := sink.wait(t, 1); got[0].body != "body" || got[0].path != "/v1/traces" {
		t.Fatalf("forwarded = %+v", got)
	}
}

func TestTraceForwardSkipsSDKRetryOfSameExport(t *testing.T) {
	sink, srv := newForwardSink()
	defer srv.Close()
	// Gregale answers 429, so the SDK retries the identical body.
	bridge, _, _ := startForwardingBridge(t, srv.URL, filepath.Join(t.TempDir(), "absent"), "", api.TraceAckLimited)
	for range 3 {
		if rec := postTraceExport(t, bridge, "application/x-protobuf", "", []byte("same")); rec.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d", rec.Code)
		}
	}
	if rec := postTraceExport(t, bridge, "application/x-protobuf", "", []byte("other")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d", rec.Code)
	}
	got := sink.wait(t, 2)
	time.Sleep(100 * time.Millisecond)
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.requests) != 2 || got[0].body == got[1].body {
		t.Fatalf("forwarded %d requests (%+v), want one per distinct export", len(sink.requests), sink.requests)
	}
}

func TestTraceForwardDedupWindowExpires(t *testing.T) {
	fwd := newTraceForwarder(api.TracingForward{Endpoint: "http://collector.invalid"}, func() (http.Header, error) { return http.Header{}, nil })
	now := time.Unix(1000, 0)
	fwd.now = func() time.Time { return now }
	if !fwd.enqueue(api.TraceLocalTracesPath, "application/x-protobuf", "", []byte("x")) {
		t.Fatal("first enqueue refused")
	}
	if fwd.enqueue(api.TraceLocalTracesPath, "application/x-protobuf", "", []byte("x")) {
		t.Fatal("retry within the window was queued again")
	}
	if !fwd.enqueue(api.TraceLocalLogsPath, "application/x-protobuf", "", []byte("x")) {
		t.Fatal("the same bytes on the other signal were treated as a duplicate")
	}
	now = now.Add(api.TraceForwardDedupWindow)
	if !fwd.enqueue(api.TraceLocalTracesPath, "application/x-protobuf", "", []byte("x")) {
		t.Fatal("export after the window was not queued")
	}
}

func TestTraceForwardRetriesTransientFailures(t *testing.T) {
	sink, srv := newForwardSink(http.StatusServiceUnavailable, http.StatusTooManyRequests)
	defer srv.Close()
	outcomes := make(chan string, 4)
	bridge, fwd, _ := startForwardingBridge(t, srv.URL, filepath.Join(t.TempDir(), "absent"), "", api.TraceAckAccepted)
	fwd.observe = func(outcome string, _ error) { outcomes <- outcome }
	postTraceExport(t, bridge, "application/x-protobuf", "", []byte("retry-me"))
	if got := sink.wait(t, 3); len(got) != 3 {
		t.Fatalf("attempts = %d, want 3", len(got))
	}
	if outcome := <-outcomes; outcome != "delivered" {
		t.Fatalf("outcome = %q, want delivered", outcome)
	}
}

func TestTraceForwardDoesNotRetryPermanentRefusal(t *testing.T) {
	sink, srv := newForwardSink(http.StatusUnauthorized)
	defer srv.Close()
	outcomes := make(chan string, 4)
	bridge, fwd, _ := startForwardingBridge(t, srv.URL, filepath.Join(t.TempDir(), "absent"), "", api.TraceAckAccepted)
	fwd.observe = func(outcome string, _ error) { outcomes <- outcome }
	postTraceExport(t, bridge, "application/x-protobuf", "", []byte("denied"))
	sink.wait(t, 1)
	if outcome := <-outcomes; outcome != "failed" {
		t.Fatalf("outcome = %q, want failed", outcome)
	}
	time.Sleep(100 * time.Millisecond)
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.requests) != 1 {
		t.Fatalf("a 401 was retried: %d attempts", len(sink.requests))
	}
}

func TestTraceForwardMissingSecretSendsNothing(t *testing.T) {
	sink, srv := newForwardSink()
	defer srv.Close()
	secrets := filepath.Join(t.TempDir(), "secrets.env")
	writeSecretsFile(t, secrets, map[string]string{"OTHER": "x"})
	outcomes := make(chan error, 4)
	bridge, fwd, _ := startForwardingBridge(t, srv.URL, secrets, "OTLP_HEADERS", api.TraceAckAccepted)
	fwd.observe = func(_ string, err error) { outcomes <- err }
	if rec := postTraceExport(t, bridge, "application/x-protobuf", "", []byte("body")); rec.Code != http.StatusOK {
		t.Fatalf("status = %d; a forwarding problem must not fail the app's export", rec.Code)
	}
	err := <-outcomes
	if err == nil || !strings.Contains(err.Error(), "OTLP_HEADERS is not set") {
		t.Fatalf("err = %v", err)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.requests) != 0 {
		t.Fatalf("forwarded without the configured headers: %+v", sink.requests)
	}
}

func TestTraceForwardQueueBoundDropsWithoutBlocking(t *testing.T) {
	fwd := newTraceForwarder(api.TracingForward{Endpoint: "http://collector.invalid"}, func() (http.Header, error) { return http.Header{}, nil })
	fwd.maxBytes = 10
	dropped := 0
	fwd.observe = func(outcome string, _ error) {
		if outcome == "dropped_queue_full" {
			dropped++
		}
	}
	if !fwd.enqueue(api.TraceLocalTracesPath, "application/x-protobuf", "", []byte("123456")) {
		t.Fatal("first export refused")
	}
	if fwd.enqueue(api.TraceLocalTracesPath, "application/x-protobuf", "", []byte("abcdef")) {
		t.Fatal("export past the byte bound was queued")
	}
	if dropped != 1 {
		t.Fatalf("dropped = %d", dropped)
	}
	var nilForwarder *traceForwarder
	if nilForwarder.enqueue(api.TraceLocalTracesPath, "application/x-protobuf", "", []byte("x")) {
		t.Fatal("a bridge without forwarding queued an export")
	}
}

func TestForwardHeaderSourceRereadsChangedSecret(t *testing.T) {
	secrets := filepath.Join(t.TempDir(), "secrets.env")
	writeSecretsFile(t, secrets, map[string]string{"H": "Authorization=old"})
	source := &forwardHeaderSource{path: secrets, name: "H"}
	h, err := source.get()
	if err != nil || h.Get("Authorization") != "old" {
		t.Fatalf("first read = %v, %v", h, err)
	}
	// A wake rewrites the file; the next export uses the rotated value.
	writeSecretsFile(t, secrets, map[string]string{"H": "Authorization=rotated-value"})
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(secrets, later, later); err != nil {
		t.Fatal(err)
	}
	h, err = source.get()
	if err != nil || h.Get("Authorization") != "rotated-value" {
		t.Fatalf("after rotation = %v, %v", h, err)
	}
	empty := &forwardHeaderSource{path: secrets}
	if h, err := empty.get(); err != nil || len(h) != 0 {
		t.Fatalf("no headers secret = %v, %v", h, err)
	}
}

func TestParseOTLPHeaders(t *testing.T) {
	h, err := parseOTLPHeaders(" api-key = a%2Cb , x-tenant=t1,,")
	if err != nil || h.Get("Api-Key") != "a,b" || h.Get("X-Tenant") != "t1" {
		t.Fatalf("parsed = %v, %v", h, err)
	}
	for _, bad := range []string{"novalue", "=v", "Host=evil", "Content-Type=text/plain", "bad key=v", "k=%zz", "k=a%0Db"} {
		if _, err := parseOTLPHeaders(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		} else if strings.Contains(err.Error(), "evil") || strings.Contains(err.Error(), "text/plain") {
			t.Fatalf("error leaks a header value: %v", err)
		}
	}
}
