package tracing

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite testdata/otlp_export.json")

// collector stands in for guest-init's OTLP bridge.
type collector struct {
	mu    sync.Mutex
	spans []otlpSpan
	types []string
}

func (c *collector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body otlpExport
	raw, _ := io.ReadAll(r.Body)
	if r.URL.Path != "/v1/traces" || json.Unmarshal(raw, &body) != nil {
		http.Error(w, "bad export", http.StatusBadRequest)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.types = append(c.types, r.Header.Get("Content-Type"))
	for _, rs := range body.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			c.spans = append(c.spans, ss.Spans...)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte("{}"))
}

func (c *collector) byName(t *testing.T, name string) otlpSpan {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.spans {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no span %q among %+v", name, c.spans)
	return otlpSpan{}
}

func attr(s otlpSpan, key string) string {
	for _, kv := range s.Attributes {
		if kv.Key == key {
			if kv.Value.StringValue != nil {
				return *kv.Value.StringValue
			}
			if kv.Value.IntValue != nil {
				return *kv.Value.IntValue
			}
		}
	}
	return ""
}

// startTracing starts the package as guest-init would configure it and
// returns the collector; flush exports everything ended so far.
func startTracing(t *testing.T, env map[string]string) (c *collector, flush func()) {
	t.Helper()
	c = &collector{}
	srv := httptest.NewServer(c)
	t.Cleanup(srv.Close)
	t.Setenv(envEnabled, "1")
	t.Setenv(envEndpoint, srv.URL+"/v1/traces")
	t.Setenv(envServiceName, "orders-api")
	for k, v := range env {
		t.Setenv(k, v)
	}
	shutdown := Start()
	if !Enabled() {
		t.Fatal("Start with FAAS_TRACING_ENABLED=1 did not enable tracing")
	}
	done := false
	flush = func() {
		if done {
			return
		}
		done = true
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdown(ctx); err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	}
	t.Cleanup(flush)
	return c, flush
}

func TestStartWithoutGuestTracingIsANoOp(t *testing.T) {
	t.Setenv(envEnabled, "")
	shutdown := Start()
	defer func() { _ = shutdown(context.Background()) }()
	if Enabled() {
		t.Fatal("tracing enabled without FAAS_TRACING_ENABLED")
	}
	var sawHeader string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawHeader = r.Header.Get("traceparent")
	}))
	defer upstream.Close()
	resp, err := (&http.Client{Transport: Transport(nil)}).Get(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if sawHeader != "" {
		t.Fatalf("traceparent %q sent while tracing is off", sawHeader)
	}
	ctx, span := StartSpan(context.Background(), "work")
	span.SetAttribute("k", "v")
	span.End()
	if span != nil || ctx != context.Background() {
		t.Fatal("StartSpan returned a span while tracing is off")
	}
}

// The request span continues the gateway's trace, the outbound call is its
// child, and the dependency receives a traceparent naming the client span.
func TestHandlerAndTransportContinueTheGatewayTrace(t *testing.T) {
	c, flush := startTracing(t, nil)
	var upstreamParent string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamParent = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusTeapot)
	}))
	defer upstream.Close()
	client := &http.Client{Transport: Transport(nil)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders/{id}", func(w http.ResponseWriter, r *http.Request) {
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, upstream.URL+"/stock?secret=s3cr3t", nil)
		req.SetBasicAuth("user", "pw")
		resp, err := client.Do(req)
		if err != nil {
			t.Errorf("upstream: %v", err)
			return
		}
		_ = resp.Body.Close()
		w.WriteHeader(http.StatusCreated)
	})
	app := httptest.NewServer(Handler(mux))
	defer app.Close()

	const gatewayTrace, gatewaySpan = "4bf92f3577b34da6a3ce929d0e0e4736", "00f067aa0ba902b7"
	req, _ := http.NewRequest(http.MethodGet, app.URL+"/orders/42", nil)
	req.Header.Set("traceparent", "00-"+gatewayTrace+"-"+gatewaySpan+"-00")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("app status = %d", resp.StatusCode)
	}
	flush()

	server := c.byName(t, "GET /orders/{id}")
	if server.TraceID != gatewayTrace || server.ParentSpanID != gatewaySpan || server.Kind != int(KindServer) {
		t.Fatalf("server span = %+v, want the gateway's trace and parent (an unsampled flag must not suppress it)", server)
	}
	if attr(server, "http.route") != "/orders/{id}" || attr(server, "http.response.status_code") != "201" || server.Status != nil {
		t.Fatalf("server span attributes = %+v status %+v", server.Attributes, server.Status)
	}
	client0 := c.byName(t, "GET")
	if client0.TraceID != gatewayTrace || client0.ParentSpanID != server.SpanID || client0.Kind != int(KindClient) {
		t.Fatalf("client span = %+v, want a child of the server span", client0)
	}
	if got := attr(client0, "url.full"); got != upstream.URL+"/stock" {
		t.Fatalf("url.full = %q, want no query string or credentials", got)
	}
	if attr(client0, "server.address") != "127.0.0.1" || attr(client0, "http.response.status_code") != "418" || client0.Status == nil {
		t.Fatalf("client span = %+v", client0)
	}
	if upstreamParent != "00-"+gatewayTrace+"-"+client0.SpanID+"-01" {
		t.Fatalf("upstream traceparent = %q, want the client span", upstreamParent)
	}
	if c.types[0] != "application/json" {
		t.Fatalf("export content type = %q", c.types[0])
	}
}

func TestHandlerRecordsPanicsAsServerErrors(t *testing.T) {
	c, flush := startTracing(t, nil)
	h := Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	func() {
		defer func() { _ = recover() }()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/pay", nil))
	}()
	flush()
	span := c.byName(t, "POST")
	if attr(span, "http.response.status_code") != "500" || span.Status == nil || span.ParentSpanID != "" {
		t.Fatalf("panicking request span = %+v", span)
	}
}

func TestSamplerRatio(t *testing.T) {
	for _, tc := range []struct {
		sampler, arg string
		want         float64
	}{
		{"", "", 1}, {"always_on", "", 1}, {"always_off", "", 0},
		{"traceidratio", "0.25", 0.25}, {"parentbased_traceidratio", "2", 1}, {"traceidratio", "nope", 1},
	} {
		if got := samplerRatio(tc.sampler, tc.arg); got != tc.want {
			t.Errorf("samplerRatio(%q, %q) = %v, want %v", tc.sampler, tc.arg, got, tc.want)
		}
	}
	tr := &tracer{ratio: 0.5}
	low, high := traceID{}, traceID{}
	low[15], high[8] = 1, 0xff
	if !tr.sample(low) || tr.sample(high) {
		t.Fatal("ratio sampling is not by trace ID")
	}
}

func TestParseTraceparent(t *testing.T) {
	good := parseTraceparent("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	if !good.valid() || !good.sampled || !good.remote {
		t.Fatalf("valid header parsed as %+v", good)
	}
	for _, bad := range []string{"", "00-00000000000000000000000000000000-00f067aa0ba902b7-01",
		"ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "00-xyz-00f067aa0ba902b7-01", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7"} {
		if parseTraceparent(bad).valid() {
			t.Errorf("parseTraceparent(%q) accepted", bad)
		}
	}
}

// The golden file is also decoded by pkg/gateway's OTLP codec, the one apid
// uses for guest frames, so the encoding is checked against the real reader.
func TestOTLPExportGolden(t *testing.T) {
	start := time.Unix(1_700_000_000, 0).UTC()
	server := &Span{name: "GET /orders/{id}", kind: KindServer, start: start, end: start.Add(20 * time.Millisecond)}
	copy(server.ctx.trace[:], []byte{0x4b, 0xf9, 0x2f, 0x35, 0x77, 0xb3, 0x4d, 0xa6, 0xa3, 0xce, 0x92, 0x9d, 0x0e, 0x0e, 0x47, 0x36})
	copy(server.ctx.span[:], []byte{1, 2, 3, 4, 5, 6, 7, 8})
	copy(server.parent[:], []byte{0x00, 0xf0, 0x67, 0xaa, 0x0b, 0xa9, 0x02, 0xb7})
	server.attrs = []attribute{stringAttr("http.route", "/orders/{id}"), intAttr("http.response.status_code", 200)}
	query := &Span{name: "SELECT", kind: KindClient, start: start.Add(2 * time.Millisecond), end: start.Add(9 * time.Millisecond), ctx: spanContext{trace: server.ctx.trace}, parent: server.ctx.span}
	copy(query.ctx.span[:], []byte{9, 10, 11, 12, 13, 14, 15, 16})
	query.attrs = []attribute{stringAttr("db.system.name", "postgresql"), stringAttr("db.query.text", "SELECT id FROM orders WHERE id = $1")}
	query.errored, query.statusMsg = true, "canceling statement"
	got, err := json.MarshalIndent(otlpRequest([]attribute{stringAttr("service.name", "orders-api")}, []*Span{server, query}), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "otlp_export.json")
	if *update {
		if err := os.WriteFile(path, append(got, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(want)) != string(got) {
		t.Fatalf("OTLP export changed; rerun with -update and check pkg/gateway's decoder test:\n%s", got)
	}
}

func TestQueueDropsWhenFull(t *testing.T) {
	e := newExporter("http://127.0.0.1:1/v1/traces", nil)
	for i := 0; i < queueSize+3; i++ {
		e.enqueue(&Span{})
	}
	if e.dropped.Load() != 3 {
		t.Fatalf("dropped = %d, want 3", e.dropped.Load())
	}
}
