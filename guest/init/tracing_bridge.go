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

// traceBridge is the guest-local OTLP/HTTP receiver (ADR-958). It accepts
// standard SDK trace and log exports on 127.0.0.1:4318 and forwards each body
// unparsed to the host; log frames carry api.TraceCodecLogsSignal. It holds
// no credential: vmmd stamps identity from the vsock peer.
type traceBridge struct {
	slots chan struct{}
	send  func(context.Context, []byte) (byte, error)
	// observe, when set, receives each forwarded export's outcome so the
	// platform side can log delivery problems (diagnostics only).
	observe func(outcome string, err error)
	// forward, when set, gets a copy of every export for the customer's own
	// OTLP endpoint (ADR-20261108100000).
	forward *traceForwarder
}

// tryAcquire takes an upload slot, shared by the OTLP/HTTP and OTLP/gRPC
// receivers; false means the bridge is busy.
func (b *traceBridge) tryAcquire() bool {
	select {
	case b.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (b *traceBridge) release() { <-b.slots }

// export hands one non-empty export to the forwarder (if any) and to the
// host, returning the host's ack. signal is 0 for traces,
// api.TraceCodecLogsSignal for logs or api.TraceCodecMetricsSignal for
// metrics; codec carries no signal bit.
func (b *traceBridge) export(ctx context.Context, codec, signal byte, contentType, contentEncoding string, body []byte) (byte, error) {
	signalPath := api.TraceLocalTracesPath
	switch signal {
	case api.TraceCodecLogsSignal:
		signalPath = api.TraceLocalLogsPath
	case api.TraceCodecMetricsSignal:
		signalPath = api.TraceLocalMetricsPath
	}
	b.forward.enqueue(signalPath, contentType, contentEncoding, body)
	ack, err := b.send(ctx, encodeTraceFrame(codec|signal, body))
	b.report(ack, err)
	return ack, err
}

func newTraceBridge(send func(context.Context, []byte) (byte, error)) *traceBridge {
	return &traceBridge{slots: make(chan struct{}, api.TraceMaxConcurrentUploads), send: send}
}

func (b *traceBridge) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+api.TraceLocalTracesPath, func(w http.ResponseWriter, r *http.Request) { b.exportHandler(w, r, 0) })
	mux.HandleFunc("POST "+api.TraceLocalLogsPath, func(w http.ResponseWriter, r *http.Request) {
		b.exportHandler(w, r, api.TraceCodecLogsSignal)
	})
	mux.HandleFunc("POST "+api.TraceLocalMetricsPath, func(w http.ResponseWriter, r *http.Request) {
		b.exportHandler(w, r, api.TraceCodecMetricsSignal)
	})
	return mux
}

// exportHandler forwards one OTLP export; signal is 0 for traces and
// api.TraceCodecLogsSignal for logs. Both responses are empty export
// responses, which encode identically for the two signals.
func (b *traceBridge) exportHandler(w http.ResponseWriter, r *http.Request, signal byte) {
	codec, mediaType, ok := traceFrameCodec(r.Header.Get("Content-Type"), r.Header.Get("Content-Encoding"))
	if !ok {
		http.Error(w, "expected application/x-protobuf or application/json", http.StatusUnsupportedMediaType)
		return
	}
	if !b.tryAcquire() {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "trace bridge busy", http.StatusTooManyRequests)
		return
	}
	defer b.release()
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
	ack, err := b.export(r.Context(), codec, signal, mediaType, traceCodecContentEncoding(codec), body)
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

// traceCodecContentEncoding is the normalized Content-Encoding of a frame
// codec: "gzip" for the gzip codecs, "" otherwise.
func traceCodecContentEncoding(codec byte) string {
	if codec == api.TraceCodecGzipProtobuf || codec == api.TraceCodecGzipJSON {
		return "gzip"
	}
	return ""
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
// the platform auto-instrumentation when present (ADR-958). Log export to the
// bridge is on with tracing (ADR-213 runtime adapter) unless the app sets its
// own OTel logs exporter or endpoint, which is left untouched.
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
	switch {
	case cfg.Metrics && !hasEnvKey(env, "OTEL_METRICS_EXPORTER") && !hasEnvKey(env, "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"):
		// tracing.metrics (ADR-20261108160000 slice B): metric exports go to
		// the bridge and become the app's custom metrics. Delta temporality
		// so several instances' counters add up instead of replacing each
		// other, unless the app chose one.
		env = setEnvValue(env, "OTEL_METRICS_EXPORTER", "otlp")
		env = setEnvValue(env, "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", api.TraceLocalEndpoint+api.TraceLocalMetricsPath)
		env = setEnvValue(env, "OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "http/protobuf")
		if !hasEnvKey(env, "OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE") {
			env = setEnvValue(env, "OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "delta")
		}
	case !hasEnvKey(env, "OTEL_METRICS_EXPORTER"):
		env = setEnvValue(env, "OTEL_METRICS_EXPORTER", "none")
	}
	if !hasEnvKey(env, "OTEL_LOGS_EXPORTER") && !hasEnvKey(env, "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT") {
		env = setEnvValue(env, "OTEL_LOGS_EXPORTER", "otlp")
		env = setEnvValue(env, "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", api.TraceLocalEndpoint+api.TraceLocalLogsPath)
		env = setEnvValue(env, "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "http/protobuf")
		// Python's SDK configurator attaches its logging handler only when
		// asked; the Node SDK reads OTEL_LOGS_EXPORTER itself.
		if !hasEnvKey(env, "OTEL_PYTHON_LOGGING_AUTO_INSTRUMENTATION_ENABLED") {
			env = setEnvValue(env, "OTEL_PYTHON_LOGGING_AUTO_INSTRUMENTATION_ENABLED", "true")
		}
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
