package api

// TracingConfig enables zero-config in-guest request tracing (ADR-934).
type TracingConfig struct {
	Enabled     bool    `json:"enabled" yaml:"enabled" toml:"enabled"`
	SampleRatio float64 `json:"sample_ratio,omitempty" yaml:"sample_ratio,omitempty" toml:"sample_ratio"`
}
