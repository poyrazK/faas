package api

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TracingConfig enables zero-config in-guest request tracing (ADR-958).
// Settings are baked into the deployment; changing them requires a redeploy.
type TracingConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled" toml:"enabled"`
	// SampleRatio is the fraction of requests whose spans the app exports.
	// Zero means the default of 1 (every request).
	SampleRatio float64 `json:"sample_ratio,omitempty" yaml:"sample_ratio,omitempty" toml:"sample_ratio"`
	// Forward, when its endpoint is set, sends a copy of every OTLP trace
	// and log export the app makes to the customer's own OTLP/HTTP backend,
	// from inside the VM (ADR-20261108100000). A value struct, so tracing
	// configs stay comparable with ==.
	Forward TracingForward `json:"forward,omitzero" yaml:"forward,omitempty" toml:"forward,omitempty"`
	// Metrics, when set, points the app's OpenTelemetry metrics exporter at
	// the in-guest bridge, which stores them as the app's custom metrics
	// (ADR-20261108160000 slice B). Opt-in: the managed auto-instrumentation
	// emits metrics of its own, which count toward the app's metric cap.
	Metrics bool `json:"metrics,omitempty" yaml:"metrics,omitempty" toml:"metrics,omitempty"`
}

// TracingForward names the customer's OTLP/HTTP endpoint. Endpoint is the
// base URL, as in OTEL_EXPORTER_OTLP_ENDPOINT: /v1/traces and /v1/logs are
// appended. HeadersSecret names an app secret holding request headers in the
// OTEL_EXPORTER_OTLP_HEADERS form (key=value,key2=value2); it is read inside
// the VM at send time, so a rotated secret applies from the next wake.
type TracingForward struct {
	Endpoint      string `json:"endpoint,omitempty" yaml:"endpoint,omitempty" toml:"endpoint,omitempty"`
	HeadersSecret string `json:"headers_secret,omitempty" yaml:"headers_secret,omitempty" toml:"headers_secret,omitempty"`
}

// Enabled reports whether exports are forwarded.
func (f TracingForward) Enabled() bool { return f.Endpoint != "" }

// SignalURL returns the endpoint URL for one OTLP signal path, such as
// TraceLocalTracesPath.
func (f TracingForward) SignalURL(path string) string {
	return strings.TrimRight(f.Endpoint, "/") + path
}

// TraceForwardMaxEndpointBytes bounds tracing.forward.endpoint.
const TraceForwardMaxEndpointBytes = 2048

// Validate checks the forward settings. The endpoint must be an absolute
// http or https URL without credentials, query or fragment: headers carry
// credentials, and the signal paths are appended to it.
func (f TracingForward) Validate() error {
	if f.Endpoint == "" {
		if f.HeadersSecret != "" {
			return fmt.Errorf("tracing.forward.headers_secret needs tracing.forward.endpoint")
		}
		return nil
	}
	if len(f.Endpoint) > TraceForwardMaxEndpointBytes {
		return fmt.Errorf("tracing.forward.endpoint must be at most %d bytes", TraceForwardMaxEndpointBytes)
	}
	u, err := url.Parse(f.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Opaque != "" {
		return fmt.Errorf("tracing.forward.endpoint must be an http:// or https:// URL")
	}
	if u.User != nil {
		return fmt.Errorf("tracing.forward.endpoint must not contain credentials; put them in headers_secret")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return fmt.Errorf("tracing.forward.endpoint must not have a query or fragment")
	}
	if path := strings.TrimRight(u.Path, "/"); strings.HasSuffix(path, TraceLocalTracesPath) || strings.HasSuffix(path, TraceLocalLogsPath) {
		return fmt.Errorf("tracing.forward.endpoint is the base URL; /v1/traces and /v1/logs are added for you")
	}
	if f.HeadersSecret != "" && ValidateEnvKey(f.HeadersSecret) != nil {
		return fmt.Errorf("tracing.forward.headers_secret %q is not a valid secret name", f.HeadersSecret)
	}
	return nil
}

// Guest tracing transport bounds (ADR-958). The frame bound applies to the
// encoded export as sent by the SDK (possibly gzip); apid separately bounds
// the decoded body with the public ingest limit. TraceMaxConcurrentUploads
// bounds one VM's frames in flight, in the guest bridge and again in vmmd;
// TraceMaxConcurrentForwards bounds a node's frames vmmd is forwarding to
// apid. vmmd takes the forward slot only after a frame was read in full, so
// a guest that opens streams and sends nothing holds only its own slots.
const (
	TraceVsockPort             = 1041
	TraceLocalListen           = "127.0.0.1:4318"
	TraceLocalEndpoint         = "http://" + TraceLocalListen
	TraceLocalTracesPath       = "/v1/traces"
	TraceLocalLogsPath         = "/v1/logs"
	TraceLocalMetricsPath      = "/v1/metrics"
	TraceMaxFrameBytes         = 512 << 10
	TraceMaxDecodedBytes       = 4 << 20
	TraceMaxConcurrentUploads  = 4
	TraceMaxConcurrentForwards = 16
	TraceTransportTimeout      = 2 * time.Second
	// TraceLocalGRPCListen is the guest-local OTLP/gRPC receiver (traces and
	// logs), served beside the OTLP/HTTP one (ADR-20261108100000).
	TraceLocalGRPCListen = "127.0.0.1:4317"
	// Forwarding bounds (ADR-20261108100000): bytes of exports waiting to be
	// forwarded, attempts and timeout per export, and the window in which an
	// SDK's retry of the same export is not forwarded twice.
	TraceForwardMaxQueuedBytes = 4 << 20
	TraceForwardMaxAttempts    = 3
	TraceForwardTimeout        = 10 * time.Second
	TraceForwardDedupWindow    = 2 * time.Minute
	TraceNodeBootstrapPath     = "/opt/gregale/tracing/node.cjs"
	TracePythonBootstrapDir    = "/opt/gregale/tracing/python"
)

// Guest tracing frame codecs. The guest bridge prefixes each forwarded body
// with one codec byte so the host never inspects HTTP headers.
const (
	TraceCodecProtobuf     byte = 0x01
	TraceCodecJSON         byte = 0x02
	TraceCodecGzipProtobuf byte = 0x11
	TraceCodecGzipJSON     byte = 0x12
)

// TraceCodecLogsSignal marks a frame that carries an OTLP logs export
// instead of a trace export (ADR-213 runtime adapter). It is a bit in the
// codec byte, so a host that predates log forwarding rejects the frame as an
// unknown codec and the SDK does not retry it.
const TraceCodecLogsSignal byte = 0x20

// Guest log forwarding bounds: records apid accepts from one export, and the
// longest message kept per record (log_events allows 16 KiB; runtime lines
// are bounded tighter so one noisy line cannot dominate the ledger).
const (
	TraceMaxLogRecordsPerExport = 100
	TraceMaxLogMessageBytes     = 4096
)

// TraceCodecMetricsSignal marks a frame that carries an OTLP metrics export
// (ADR-20261108160000 slice B). Like the logs bit, a host that predates it
// rejects the frame as an unknown codec, which the SDK does not retry.
const TraceCodecMetricsSignal byte = 0x40

// SplitTraceCodec separates a frame's codec byte into its payload codec and
// whether it carries logs. A metrics frame keeps its signal bit in base, so
// a caller that only knows traces and logs rejects it as an unknown codec.
func SplitTraceCodec(codec byte) (base byte, logs bool) {
	return codec &^ TraceCodecLogsSignal, codec&TraceCodecLogsSignal != 0
}

// TraceFrameSignal separates a frame's codec byte into its payload codec and
// its signal: 0 for traces, TraceCodecLogsSignal or TraceCodecMetricsSignal.
// ok is false when both signal bits are set.
func TraceFrameSignal(codec byte) (base, signal byte, ok bool) {
	logs, metrics := codec&TraceCodecLogsSignal != 0, codec&TraceCodecMetricsSignal != 0
	base = codec &^ (TraceCodecLogsSignal | TraceCodecMetricsSignal)
	switch {
	case logs && metrics:
		return base, 0, false
	case logs:
		return base, TraceCodecLogsSignal, true
	case metrics:
		return base, TraceCodecMetricsSignal, true
	}
	return base, 0, true
}

// Guest tracing ack bytes, written once by the host per frame. The guest
// bridge maps them to OTLP/HTTP statuses so SDK exporters retry only
// transient failures.
const (
	TraceAckAccepted    byte = 0
	TraceAckRejected    byte = 1
	TraceAckLimited     byte = 2
	TraceAckUnavailable byte = 3
)

// TraceCodecMediaType returns the OTLP media type and content encoding for a
// frame codec byte.
func TraceCodecMediaType(codec byte) (contentType, contentEncoding string, ok bool) {
	switch codec {
	case TraceCodecProtobuf:
		return "application/x-protobuf", "", true
	case TraceCodecJSON:
		return "application/json", "", true
	case TraceCodecGzipProtobuf:
		return "application/x-protobuf", "gzip", true
	case TraceCodecGzipJSON:
		return "application/json", "gzip", true
	}
	return "", "", false
}

// EffectiveSampleRatio returns the configured ratio, defaulting to 1.
func (c *TracingConfig) EffectiveSampleRatio() float64 {
	if c == nil || c.SampleRatio == 0 {
		return 1
	}
	return c.SampleRatio
}

// Validate checks the config against the plan. Tracing feeds the ADR-127
// request telemetry, so it is available exactly where that is.
func (c *TracingConfig) Validate(plan Plan) error {
	if c == nil {
		return nil
	}
	if c.Enabled && !MustLimitsFor(plan).DebugTelemetryEnabled {
		return fmt.Errorf("request tracing is not included on this plan")
	}
	if r := c.SampleRatio; r < 0 || r > 1 || r != r {
		return fmt.Errorf("tracing.sample_ratio must be greater than 0 and at most 1")
	}
	if err := c.Forward.Validate(); err != nil {
		return err
	}
	if c.Forward.Enabled() && !c.Enabled {
		return fmt.Errorf("tracing.forward needs tracing.enabled: true")
	}
	if c.Metrics && !c.Enabled {
		return fmt.Errorf("tracing.metrics needs tracing.enabled: true")
	}
	if c.Metrics && !MustLimitsFor(plan).CustomMetricsAllowed {
		return fmt.Errorf("custom metrics are not included on this plan")
	}
	return nil
}

// IngestGuestSpans and IngestGuestLogs outcomes shared by apid (producer)
// and vmmd (which maps them to guest ack bytes).
const (
	TraceIngestAccepted    = "accepted"
	TraceIngestRateLimited = "rate_limited"
	TraceIngestDisabled    = "disabled"
	TraceIngestInvalid     = "invalid"
	TraceIngestUnavailable = "unavailable"
)

// TraceAckForIngestOutcome maps an apid outcome to the guest ack byte.
// Unknown outcomes are permanent rejections so SDKs do not retry them.
func TraceAckForIngestOutcome(outcome string) byte {
	switch outcome {
	case TraceIngestAccepted:
		return TraceAckAccepted
	case TraceIngestRateLimited:
		return TraceAckLimited
	case TraceIngestUnavailable:
		return TraceAckUnavailable
	}
	return TraceAckRejected
}
