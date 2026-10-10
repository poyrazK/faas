package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func postTraceExport(t *testing.T, b *traceBridge, contentType, encoding string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, api.TraceLocalTracesPath, bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if encoding != "" {
		req.Header.Set("Content-Encoding", encoding)
	}
	rec := httptest.NewRecorder()
	b.handler().ServeHTTP(rec, req)
	return rec
}

func TestTraceBridgeForwardsFrame(t *testing.T) {
	var got []byte
	b := newTraceBridge(func(_ context.Context, frame []byte) (byte, error) {
		got = frame
		return api.TraceAckAccepted, nil
	})
	rec := postTraceExport(t, b, "application/x-protobuf", "gzip", []byte("payload"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 || rec.Header().Get("Content-Type") != "application/x-protobuf" {
		t.Fatalf("unexpected protobuf response %q %q", rec.Body.String(), rec.Header().Get("Content-Type"))
	}
	if len(got) != 5+len("payload") {
		t.Fatalf("frame length = %d", len(got))
	}
	if n := binary.BigEndian.Uint32(got[:4]); n != uint32(1+len("payload")) {
		t.Fatalf("frame header = %d", n)
	}
	if got[4] != api.TraceCodecGzipProtobuf || string(got[5:]) != "payload" {
		t.Fatalf("frame body = %#x %q", got[4], got[5:])
	}
}

func TestTraceBridgeJSONResponse(t *testing.T) {
	b := newTraceBridge(func(context.Context, []byte) (byte, error) { return api.TraceAckAccepted, nil })
	rec := postTraceExport(t, b, "application/json; charset=utf-8", "", []byte(`{"resourceSpans":[]}`))
	if rec.Code != http.StatusOK || rec.Body.String() != "{}" {
		t.Fatalf("response = %d %q", rec.Code, rec.Body.String())
	}
}

func TestTraceBridgeStatusMapping(t *testing.T) {
	for _, tc := range []struct {
		name string
		ack  byte
		err  error
		want int
	}{
		{name: "limited", ack: api.TraceAckLimited, want: http.StatusTooManyRequests},
		{name: "unavailable", ack: api.TraceAckUnavailable, want: http.StatusServiceUnavailable},
		{name: "rejected", ack: api.TraceAckRejected, want: http.StatusBadRequest},
		{name: "unknown ack", ack: 0x7f, want: http.StatusBadRequest},
		{name: "transport error", err: errors.New("vsock down"), want: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newTraceBridge(func(context.Context, []byte) (byte, error) { return tc.ack, tc.err })
			rec := postTraceExport(t, b, "application/x-protobuf", "", []byte("x"))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestTraceBridgeRejectsBeforeSending(t *testing.T) {
	sent := false
	b := newTraceBridge(func(context.Context, []byte) (byte, error) { sent = true; return api.TraceAckAccepted, nil })
	for _, tc := range []struct {
		name, contentType, encoding string
		body                        []byte
		want                        int
	}{
		{name: "missing content type", body: []byte("x"), want: http.StatusUnsupportedMediaType},
		{name: "unsupported media", contentType: "text/plain", body: []byte("x"), want: http.StatusUnsupportedMediaType},
		{name: "unsupported encoding", contentType: "application/json", encoding: "br", body: []byte("x"), want: http.StatusUnsupportedMediaType},
		{name: "too large", contentType: "application/x-protobuf", body: bytes.Repeat([]byte("x"), api.TraceMaxFrameBytes+1), want: http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := postTraceExport(t, b, tc.contentType, tc.encoding, tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
	if rec := postTraceExport(t, b, "application/x-protobuf", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("empty export status = %d, want 200", rec.Code)
	}
	if sent {
		t.Fatal("rejected or empty export reached the host")
	}
}

func TestTraceBridgeBusy(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, api.TraceMaxConcurrentUploads)
	b := newTraceBridge(func(context.Context, []byte) (byte, error) {
		entered <- struct{}{}
		<-release
		return api.TraceAckAccepted, nil
	})
	done := make(chan struct{})
	for i := 0; i < api.TraceMaxConcurrentUploads; i++ {
		go func() {
			postTraceExport(t, b, "application/x-protobuf", "", []byte("x"))
			done <- struct{}{}
		}()
	}
	for i := 0; i < api.TraceMaxConcurrentUploads; i++ {
		<-entered
	}
	if rec := postTraceExport(t, b, "application/x-protobuf", "", []byte("x")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	close(release)
	for i := 0; i < api.TraceMaxConcurrentUploads; i++ {
		<-done
	}
}

func TestTraceBridgeRejectsOtherRoutes(t *testing.T) {
	b := newTraceBridge(func(context.Context, []byte) (byte, error) { return api.TraceAckAccepted, nil })
	req := httptest.NewRequest(http.MethodPost, "/v1/metrics", strings.NewReader("x"))
	req.Header.Set("Content-Type", "application/x-protobuf")
	rec := httptest.NewRecorder()
	b.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestTracingEnvAtPaths(t *testing.T) {
	all := func(string) bool { return true }
	none := func(string) bool { return false }
	enabled := &api.TracingConfig{Enabled: true}

	t.Run("disabled is untouched", func(t *testing.T) {
		env := []string{"A=1"}
		got := tracingEnvAtPaths(env, &api.TracingConfig{}, "/n.cjs", "/py", all)
		if strings.Join(got, ",") != "A=1" {
			t.Fatalf("env = %v", got)
		}
		if got := tracingEnvAtPaths(env, nil, "/n.cjs", "/py", all); strings.Join(got, ",") != "A=1" {
			t.Fatalf("nil env = %v", got)
		}
	})

	t.Run("defaults and preloads", func(t *testing.T) {
		got := tracingEnvAtPaths([]string{"NODE_OPTIONS=--max-old-space-size=256", "PYTHONPATH=/app"}, enabled, "/n.cjs", "/py", all)
		for key, want := range map[string]string{
			"OTEL_TRACES_EXPORTER":               "otlp",
			"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://127.0.0.1:4318/v1/traces",
			"OTEL_EXPORTER_OTLP_TRACES_PROTOCOL": "http/protobuf",
			"OTEL_METRICS_EXPORTER":              "none",
			"OTEL_LOGS_EXPORTER":                 "none",
			"OTEL_TRACES_SAMPLER":                "always_on",
			"FAAS_TRACING_ENABLED":               "1",
			"NODE_OPTIONS":                       "--max-old-space-size=256 --require=/n.cjs",
			"PYTHONPATH":                         "/py:/app",
		} {
			if v := envValue(got, key); v != want {
				t.Fatalf("%s = %q, want %q", key, v, want)
			}
		}
		if hasEnvKey(got, "OTEL_TRACES_SAMPLER_ARG") {
			t.Fatal("always_on must not set a sampler arg")
		}
	})

	t.Run("ratio sampler", func(t *testing.T) {
		got := tracingEnvAtPaths(nil, &api.TracingConfig{Enabled: true, SampleRatio: 0.25}, "/n.cjs", "/py", none)
		if envValue(got, "OTEL_TRACES_SAMPLER") != "traceidratio" || envValue(got, "OTEL_TRACES_SAMPLER_ARG") != "0.25" {
			t.Fatalf("sampler env = %v", got)
		}
		if hasEnvKey(got, "NODE_OPTIONS") || hasEnvKey(got, "PYTHONPATH") {
			t.Fatalf("missing assets must not be preloaded: %v", got)
		}
	})

	t.Run("app choices win", func(t *testing.T) {
		got := tracingEnvAtPaths([]string{"OTEL_TRACES_SAMPLER=parentbased_traceidratio", "OTEL_METRICS_EXPORTER=prometheus"}, enabled, "/n.cjs", "/py", none)
		if envValue(got, "OTEL_TRACES_SAMPLER") != "parentbased_traceidratio" || envValue(got, "OTEL_METRICS_EXPORTER") != "prometheus" {
			t.Fatalf("app sampler/metrics overridden: %v", got)
		}
	})

	for _, key := range []string{"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_TRACES_EXPORTER"} {
		t.Run("custom exporter "+key, func(t *testing.T) {
			env := []string{key + "=https://collector.example", "NODE_OPTIONS=--trace-warnings"}
			got := tracingEnvAtPaths(env, enabled, "/n.cjs", "/py", all)
			if strings.Join(got, ",") != strings.Join(env, ",") {
				t.Fatalf("custom exporter env changed: %v", got)
			}
		})
	}
}
