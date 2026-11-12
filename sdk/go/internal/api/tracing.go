package api

// TracingConfig enables zero-config in-guest request tracing (ADR-958).
type TracingConfig struct {
	Enabled     bool           `json:"enabled" yaml:"enabled" toml:"enabled"`
	SampleRatio float64        `json:"sample_ratio,omitempty" yaml:"sample_ratio,omitempty" toml:"sample_ratio"`
	Forward     TracingForward `json:"forward,omitzero" yaml:"forward,omitempty" toml:"forward,omitempty"`
}

// TracingForward sends a copy of the app's OTLP exports to the customer's
// own OTLP/HTTP endpoint (ADR-20261108100000).
type TracingForward struct {
	Endpoint      string `json:"endpoint,omitempty" yaml:"endpoint,omitempty" toml:"endpoint,omitempty"`
	HeadersSecret string `json:"headers_secret,omitempty" yaml:"headers_secret,omitempty" toml:"headers_secret,omitempty"`
}
