package api

import (
	"fmt"
	"time"
)

// TracingConfig enables zero-config in-guest request tracing (ADR-958).
// Settings are baked into the deployment; changing them requires a redeploy.
type TracingConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled" toml:"enabled"`
	// SampleRatio is the fraction of requests whose spans the app exports.
	// Zero means the default of 1 (every request).
	SampleRatio float64 `json:"sample_ratio,omitempty" yaml:"sample_ratio,omitempty" toml:"sample_ratio"`
}

// Guest tracing transport bounds (ADR-958). The frame bound applies to the
// encoded export as sent by the SDK (possibly gzip); apid separately bounds
// the decoded body with the public ingest limit.
const (
	TraceVsockPort            = 1041
	TraceLocalListen          = "127.0.0.1:4318"
	TraceLocalEndpoint        = "http://" + TraceLocalListen
	TraceLocalTracesPath      = "/v1/traces"
	TraceMaxFrameBytes        = 512 << 10
	TraceMaxDecodedBytes      = 4 << 20
	TraceMaxConcurrentUploads = 4
	TraceTransportTimeout     = 2 * time.Second
	TraceNodeBootstrapPath    = "/opt/gregale/tracing/node.cjs"
	TracePythonBootstrapDir   = "/opt/gregale/tracing/python"
)

// Guest tracing frame codecs. The guest bridge prefixes each forwarded body
// with one codec byte so the host never inspects HTTP headers.
const (
	TraceCodecProtobuf     byte = 0x01
	TraceCodecJSON         byte = 0x02
	TraceCodecGzipProtobuf byte = 0x11
	TraceCodecGzipJSON     byte = 0x12
)

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
	return nil
}

// IngestGuestSpans outcomes shared by apid (producer) and vmmd (which maps
// them to guest ack bytes).
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
