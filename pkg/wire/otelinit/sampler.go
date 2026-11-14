// Package otelinit — sampler.go holds the per-deployment sampler for
// issue #555 acceptance #5: a new deployment's first 100 root spans
// are 100% sampled, then the sampler delegates to the existing
// head-ratio sampler (OTEL_TRACES_SAMPLER_ARG).
//
// Architecture (issue #555 PR-6):
//
//	sampler chain = ParentBased(DeploymentAware(TraceIDRatioBased(rate)))
//
// ParentBased is the outer wrapper so a child span inherits the
// parent's SampledFlag (preserves the W3C parent-trace invariant).
// DeploymentAware is the inner wrapper; it only sees root spans
// (ParentContext carries no valid SpanContext) and either forces a
// RecordAndSample (when the per-deployment counter is inside the
// window) or delegates to TraceIDRatioBased.
//
// The per-deployment counter is keyed on deployment_id lifted from
// the OTel span's attributes (`attribute.String("deployment_id",
// ...)`). schedd's `sched.wake` span already stamps this attribute
// (pkg/sched/engine.go:1351-1369), so the sampler consults the
// counter at the only span that matters for the window.
//
// The counter is reset on the "last live instance parked for this
// deployment" transition by an out-of-band watcher
// (pkg/sched/deployment_counter_watcher.go) that subscribes to the
// in-process Platform `wake` topic. Reset is therefore event-driven,
// not lazy, so a redeployment immediately enjoys the 100% window.
package otelinit

import (
	"fmt"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// DefaultWindowSize is the per-deployment 100% sampling window
// mandated by issue #555 acceptance #5.
const DefaultWindowSize = 100

// deploymentAttrKey is the OTel span attribute key the sampler
// inspects for the per-deployment override. Must match the
// attribute key schedd stamps on its `sched.wake` span
// (pkg/sched/engine.go:1351-1369).
const deploymentAttrKey = "deployment_id"

// DeploymentCounter is the per-deployment request counter the
// DeploymentAware sampler consults. The counter is keyed on
// deployment_id; values are sampled-root-span counts. The map is
// guarded by a sync.Mutex — the sampler is on the wake hot path
// (~100s wakes/sec on a one-box), and the map cardinality is bounded
// by the number of distinct deployments since the daemon started
// (human-paced turnover).
//
// The zero value is NOT ready to use; construct via NewDeploymentCounter.
type DeploymentCounter struct {
	mu         sync.Mutex
	counts     map[string]int
	windowSize int
}

// NewDeploymentCounter returns a counter with the given window size.
// windowSize <= 0 falls back to DefaultWindowSize.
func NewDeploymentCounter(windowSize int) *DeploymentCounter {
	if windowSize <= 0 {
		windowSize = DefaultWindowSize
	}
	return &DeploymentCounter{
		counts:     make(map[string]int),
		windowSize: windowSize,
	}
}

// WindowSize returns the configured 100% sampling window. Exported
// for the deployment_counter_watcher's metrics label.
func (c *DeploymentCounter) WindowSize() int {
	if c == nil {
		return DefaultWindowSize
	}
	return c.windowSize
}

// Observe atomically increments the per-deployment counter and
// reports whether the observation falls inside the 100% sampling
// window.
//
// Returns:
//   - post-increment count (0 when deploymentID is empty — the
//     sampler treats an absent deployment_id as "no override"),
//   - sampled=true iff the post-increment count is <= windowSize.
//
// The function is safe for concurrent use; multiple goroutines
// observing the same deployment_id contend on the mutex.
func (c *DeploymentCounter) Observe(deploymentID string) (count int, sampled bool) {
	if c == nil || deploymentID == "" {
		return 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts[deploymentID]++
	return c.counts[deploymentID], c.counts[deploymentID] <= c.windowSize
}

// Reset zeroes the counter for one deployment_id. Called by the
// deployment_counter_watcher on the "last live instance parked"
// transition (issue #555 acceptance #5 reset trigger).
//
// Reset on an unknown deployment_id is a no-op — the next Observe
// starts fresh from zero, which is the desired semantic.
func (c *DeploymentCounter) Reset(deploymentID string) {
	if c == nil || deploymentID == "" {
		return
	}
	c.mu.Lock()
	delete(c.counts, deploymentID)
	c.mu.Unlock()
}

// Len returns the current number of deployments with a non-zero
// counter. Exported for ops metrics + test assertions; not on the
// hot path.
func (c *DeploymentCounter) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.counts)
}

// DeploymentAware is a sdktrace.Sampler wrapper that overrides the
// head-ratio decision for the first N root spans observed for each
// deployment_id.
//
// Semantics (call order matters — read in order):
//
//  1. If the parent context carries a valid SpanContext, the parent
//     has already decided whether the trace is sampled; we must
//     honour that decision so a child span under a sampled parent
//     is always sampled (W3C TraceContext invariant). We delegate
//     to the wrapped root sampler — the underlying ParentBased on
//     the outside translates this into RecordAndSample / Drop per
//     the parent's flag.
//
//  2. If the parent context is empty (root span) and the
//     per-deployment counter (consulted via c.Observe(depID)) is
//     inside the window, force RecordAndSample so the trace lands
//     in the export pipeline regardless of the head ratio.
//
//  3. Otherwise (root span, outside the window OR no deployment_id
//     attribute), delegate to the wrapped root sampler. The wrapped
//     sampler is the TraceIDRatioBased instance; OTel's
//     deterministic-on-TraceID sampling applies as usual.
//
// The sampler is intended to be wrapped in sdktrace.ParentBased at
// construction time (see otelinit.Init). ParentBased guarantees the
// parent's flag is honoured on the way out.
type DeploymentAware struct {
	root    sdktrace.Sampler
	counter *DeploymentCounter
}

// DeploymentAwareOption configures the sampler at construction.
type DeploymentAwareOption func(*DeploymentAware)

// WithCounter plugs a pre-existing DeploymentCounter (used by
// otelinit.Init so the watcher can reset the same counter the
// sampler consults). Without this option, NewDeploymentAware
// allocates its own counter — fine for tests; NOT what production
// wants (the watcher would reset a different counter).
func WithCounter(c *DeploymentCounter) DeploymentAwareOption {
	return func(s *DeploymentAware) { s.counter = c }
}

// NewDeploymentAware wraps root in the per-deployment override.
// root must be non-nil; passing nil panics (matches sdktrace
// conventions where a nil root would crash on first ShouldSample).
func NewDeploymentAware(root sdktrace.Sampler, opts ...DeploymentAwareOption) *DeploymentAware {
	if root == nil {
		panic("otelinit: NewDeploymentAware: root sampler is required")
	}
	s := &DeploymentAware{
		root:    root,
		counter: NewDeploymentCounter(DefaultWindowSize),
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.counter == nil {
		s.counter = NewDeploymentCounter(DefaultWindowSize)
	}
	return s
}

// ShouldSample implements sdktrace.Sampler.
//
// The function is intentionally short — the documentation above is
// the source of truth for the decision tree. Any change here MUST
// update the package doc-comment on sampler.go in lockstep.
func (s *DeploymentAware) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	// Step 1: honour a parent's decision. OTel defines "valid
	// SpanContext" via SpanContextFromContext — a remote parent
	// or a local parent both count. ParentBased (the outer
	// wrapper) eventually enforces the flag; we delegate so the
	// outer wrapper's branch logic sees the same answer.
	parent := oteltrace.SpanContextFromContext(p.ParentContext)
	if parent.IsValid() {
		return s.root.ShouldSample(p)
	}

	// Step 2 / 3: root span — inspect attributes for deployment_id.
	depID := extractDeploymentID(p.Attributes)
	if depID == "" {
		// No deployment_id attribute → fall back to the wrapped
		// root sampler. This matches the contract for non-schedd
		// spans (gatewayd-internal.handler, vmmd.create_*, etc.) that do
		// not stamp deployment_id today; their sampling decision
		// remains the head-ratio decision.
		return s.root.ShouldSample(p)
	}
	_, inside := s.counter.Observe(depID)
	if inside {
		return sdktrace.SamplingResult{
			Decision:   sdktrace.RecordAndSample,
			Tracestate: oteltrace.SpanContextFromContext(p.ParentContext).TraceState(),
		}
	}
	return s.root.ShouldSample(p)
}

// Description returns the OTel SDK's sampler description string.
// Required by the Sampler interface. The boot log line in
// otelinit.go ("sampler", "sampler_arg", "window_size") is the
// canonical summary; Description() exists only because the
// interface requires it.
func (s *DeploymentAware) Description() string {
	return fmt.Sprintf("DeploymentAware(window=%d)", s.windowSize())
}

func (s *DeploymentAware) windowSize() int {
	if s.counter == nil {
		return DefaultWindowSize
	}
	return s.counter.WindowSize()
}

// extractDeploymentID walks the attribute slice and returns the
// value of the first matching string attribute. The OTel SDK's
// attribute.KeyValue.Filter API would be more idiomatic but
// allocates per call; a hand-rolled walk is cheaper and the slice
// is bounded by the number of attributes the caller stamped.
func extractDeploymentID(attrs []attribute.KeyValue) string {
	for _, kv := range attrs {
		if kv.Key == deploymentAttrKey && kv.Value.Type() == attribute.STRING {
			return kv.Value.AsString()
		}
	}
	return ""
}

// DependencySpanAttribute marks a span as a call to a dependency
// (gregale.dependency.type). Such spans feed platform views that are not a
// sample: the retained service-proxy and outbound spans gatewayd-internal
// writes through apid, and the service map built from them.
const DependencySpanAttribute = "gregale.dependency.type"

// dependencyAlways records every dependency span and leaves every other span
// to next. A dependency span is often the child of a request span the head
// ratio dropped (the caller forwards the gateway's traceparent), so next's
// parent-based rule alone would drop it too.
type dependencyAlways struct{ next sdktrace.Sampler }

// NewDependencyAlways wraps next so dependency spans are always recorded.
func NewDependencyAlways(next sdktrace.Sampler) sdktrace.Sampler {
	return dependencyAlways{next: next}
}

func (s dependencyAlways) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	for _, a := range p.Attributes {
		if a.Key == DependencySpanAttribute {
			return sdktrace.SamplingResult{
				Decision:   sdktrace.RecordAndSample,
				Tracestate: oteltrace.SpanContextFromContext(p.ParentContext).TraceState(),
			}
		}
	}
	return s.next.ShouldSample(p)
}

func (s dependencyAlways) Description() string {
	return fmt.Sprintf("DependencyAlways{%s}", s.next.Description())
}
