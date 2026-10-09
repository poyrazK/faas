package main

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// traceBridge is the guest-local OTLP/HTTP receiver (ADR-829). It accepts
// standard SDK exports on 127.0.0.1:4318 and forwards each body unparsed to
// the host. It holds no credential: vmmd stamps identity from the vsock peer.
type traceBridge struct {
	slots chan struct{}
	send  func(context.Context, []byte) (byte, error)
	// observe, when set, receives each forwarded export's outcome so the
	// platform side can log delivery problems (diagnostics only).
	observe func(outcome string, err error)
}

func newTraceBridge(send func(context.Context, []byte) (byte, error)) *traceBridge {
	return &traceBridge{slots: make(chan struct{}, api.TraceMaxConcurrentUploads), send: send}
}

func (b *traceBridge) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+api.TraceLocalTracesPath, b.exportHandler)
	return mux
}

func (b *traceBridge) exportHandler(w http.ResponseWriter, r *http.Request) {
	codec, mediaType, ok := traceFrameCodec(r.Header.Get("Content-Type"), r.Header.Get("Content-Encoding"))
	if !ok {
		http.Error(w, "expected application/x-protobuf or application/json", http.StatusUnsupportedMediaType)
		return
	}
	select {
	case b.slots <- struct{}{}:
		defer func() { <-b.slots }()
	default:
		w.Header().Set("Retry-After", "1")
		http.Error(w, "trace bridge busy", http.StatusTooManyRequests)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, api.TraceMaxFrameBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "trace export exceeds bounds", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "read trace export", http.StatusBadRequest)
		return
	}
	if len(body) == 0 {
		writeTraceExportResponse(w, mediaType)
		return
	}
	ack, err := b.send(r.Context(), encodeTraceFrame(codec, body))
	b.report(ack, err)
	if err != nil {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "trace host unavailable", http.StatusServiceUnavailable)
		return
	}
	switch ack {
	case api.TraceAckAccepted:
		writeTraceExportResponse(w, mediaType)
	case api.TraceAckLimited:
		w.Header().Set("Retry-After", "1")
		http.Error(w, "trace export rate limited", http.StatusTooManyRequests)
	case api.TraceAckUnavailable:
		w.Header().Set("Retry-After", "1")
		http.Error(w, "trace host unavailable", http.StatusServiceUnavailable)
	default:
		// Permanent refusals (tracing disabled, malformed export) must not
		// be retried by the SDK; 400 is non-retryable in OTLP/HTTP.
		http.Error(w, "trace export rejected", http.StatusBadRequest)
	}
}

func (b *traceBridge) report(ack byte, err error) {
	if b.observe == nil {
		return
	}
	outcome := "rejected"
	switch {
	case err != nil:
		outcome = "transport"
	case ack == api.TraceAckAccepted:
		outcome = "accepted"
	case ack == api.TraceAckLimited:
		outcome = "limited"
	case ack == api.TraceAckUnavailable:
		outcome = "unavailable"
	}
	b.observe(outcome, err)
}

// traceFrameCodec maps OTLP/HTTP headers to the one-byte frame codec. A
// missing Content-Type is rejected: SDK exporters always send one.
func traceFrameCodec(contentType, contentEncoding string) (byte, string, bool) {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return 0, "", false
	}
	gzipped := false
	switch enc := strings.ToLower(strings.TrimSpace(contentEncoding)); enc {
	case "", "identity":
	case "gzip":
		gzipped = true
	default:
		return 0, "", false
	}
	switch {
	case mediaType == "application/x-protobuf" && gzipped:
		return api.TraceCodecGzipProtobuf, mediaType, true
	case mediaType == "application/x-protobuf":
		return api.TraceCodecProtobuf, mediaType, true
	case mediaType == "application/json" && gzipped:
		return api.TraceCodecGzipJSON, mediaType, true
	case mediaType == "application/json":
		return api.TraceCodecJSON, mediaType, true
	}
	return 0, "", false
}

// encodeTraceFrame builds [4B BE length][1B codec][body], where length
// counts the codec byte and body.
func encodeTraceFrame(codec byte, body []byte) []byte {
	frame := make([]byte, 5+len(body))
	binary.BigEndian.PutUint32(frame[:4], uint32(1+len(body)))
	frame[4] = codec
	copy(frame[5:], body)
	return frame
}

// writeTraceExportResponse writes an empty ExportTraceServiceResponse, which
// is zero bytes in protobuf and an empty object in JSON.
func writeTraceExportResponse(w http.ResponseWriter, mediaType string) {
	w.Header().Set("Content-Type", mediaType)
	if mediaType == "application/json" {
		w.Header().Set("Content-Length", strconv.Itoa(2))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
		return
	}
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusOK)
}

// stampTracingEnv points managed OTel SDKs at the local bridge and preloads
// the platform auto-instrumentation when present (ADR-829).
func stampTracingEnv(env []string, cfg *api.TracingConfig) []string {
	return tracingEnvAtPaths(env, cfg, api.TraceNodeBootstrapPath, api.TracePythonBootstrapDir, pathExists)
}

// tracingEnvAtPaths leaves an app that already configures its own trace
// exporter completely untouched: no endpoint, sampler or preload changes.
func tracingEnvAtPaths(env []string, cfg *api.TracingConfig, nodePath, pythonPath string, exists func(string) bool) []string {
	if cfg == nil || !cfg.Enabled {
		return env
	}
	if hasEnvKey(env, "OTEL_EXPORTER_OTLP_ENDPOINT") || hasEnvKey(env, "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") || hasEnvKey(env, "OTEL_TRACES_EXPORTER") {
		return env
	}
	env = setEnvValue(env, "OTEL_TRACES_EXPORTER", "otlp")
	env = setEnvValue(env, "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", api.TraceLocalEndpoint+api.TraceLocalTracesPath)
	env = setEnvValue(env, "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "http/protobuf")
	// The bootstraps start an SDK only when this is set.
	env = setEnvValue(env, "FAAS_TRACING_ENABLED", "1")
	if !hasEnvKey(env, "OTEL_METRICS_EXPORTER") {
		env = setEnvValue(env, "OTEL_METRICS_EXPORTER", "none")
	}
	if !hasEnvKey(env, "OTEL_LOGS_EXPORTER") {
		env = setEnvValue(env, "OTEL_LOGS_EXPORTER", "none")
	}
	if !hasEnvKey(env, "OTEL_TRACES_SAMPLER") {
		// The gateway head-samples its own spans; the parent's sampled flag
		// must not suppress the app's request spans, so the sampler is not
		// parent-based.
		if ratio := cfg.EffectiveSampleRatio(); ratio >= 1 {
			env = setEnvValue(env, "OTEL_TRACES_SAMPLER", "always_on")
		} else {
			env = setEnvValue(env, "OTEL_TRACES_SAMPLER", "traceidratio")
			env = setEnvValue(env, "OTEL_TRACES_SAMPLER_ARG", strconv.FormatFloat(ratio, 'f', -1, 64))
		}
	}
	if exists(nodePath) {
		env = setEnvValue(env, "NODE_OPTIONS", strings.TrimSpace(envValue(env, "NODE_OPTIONS")+" --require="+nodePath))
	}
	if exists(pythonPath) {
		pythonPathValue := pythonPath
		if current := envValue(env, "PYTHONPATH"); current != "" {
			pythonPathValue += ":" + current
		}
		env = setEnvValue(env, "PYTHONPATH", pythonPathValue)
	}
	return env
}

func hasEnvKey(env []string, key string) bool {
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); ok && k == key {
			return true
		}
	}
	return false
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
