// Package profileproto is the small guest-to-host CPU profiling wire contract.
package profileproto

type Upload struct {
	RouteRequests *RouteRequestReport `json:"route_requests,omitempty"`
	// ProcessID separates identical captures from different instrumented
	// processes in one VM. The host hashes it with its VM lifetime to derive
	// an opaque collector ID; it cannot select a tenant or deployment.
	ProcessID string `json:"process_id,omitempty"`
	// Kind is empty or "cpu" for CPU samples and "heap" for live heap
	// samples (ADR-967). Older guests omit it.
	Kind          string `json:"kind,omitempty"`
	Profile       []byte `json:"profile"`
	FromUnixNano  int64  `json:"from_unix_nano,omitempty"`
	UntilUnixNano int64  `json:"until_unix_nano,omitempty"`
}

// RouteRequestReport is an application-reported census during a CPU capture.
// It cannot select host identity or prove request instrumentation completeness.
type RouteRequestReport struct {
	FromUnixNano  int64            `json:"from_unix_nano"`
	UntilUnixNano int64            `json:"until_unix_nano"`
	Complete      bool             `json:"complete"`
	Routes        map[string]int64 `json:"routes"`
}
