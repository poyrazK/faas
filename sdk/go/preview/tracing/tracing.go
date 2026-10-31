// Package tracing sends a Go app's request, HTTP client and database spans to
// the Gregale debugger without an OpenTelemetry dependency (ADR-958
// amendment).
//
//	func main() {
//		shutdown := tracing.Start()
//		defer shutdown(context.Background())
//
//		http.DefaultTransport = tracing.Transport(http.DefaultTransport)
//		db, err := tracing.Open("pgx", os.Getenv("DATABASE_URL"), "postgresql")
//		...
//		http.ListenAndServe(":"+os.Getenv("PORT"), tracing.Handler(mux))
//	}
//
// Tracing is on only when the app's manifest sets tracing.enabled: guest-init
// then sets FAAS_TRACING_ENABLED=1 and the OTLP endpoint of its local bridge.
// Everywhere else (local runs, tests, apps with tracing off) Start does
// nothing and the wrappers pass calls straight through.
//
// Spans are linked through context.Context, so pass the request's context to
// outbound calls (http.NewRequestWithContext, db.QueryContext and
// db.ExecContext) for them to appear under the request.
package tracing

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	envEnabled      = "FAAS_TRACING_ENABLED"
	envEndpoint     = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	envSampler      = "OTEL_TRACES_SAMPLER"
	envSamplerArg   = "OTEL_TRACES_SAMPLER_ARG"
	envServiceName  = "OTEL_SERVICE_NAME"
	defaultEndpoint = "http://127.0.0.1:4318/v1/traces"
	scopeName       = "github.com/poyrazK/faas/sdk/go/preview/tracing"
)

// tracer is the process-wide state created by Start.
type tracer struct {
	ratio    float64
	exporter *exporter
}

var active atomic.Pointer[tracer]

// Start turns tracing on when guest-init enabled it for this app and returns
// a function that flushes the remaining spans and turns it off. Call it once,
// before serving requests. Without FAAS_TRACING_ENABLED=1, or when tracing is
// already started, the returned function does nothing.
func Start() func(context.Context) error {
	if os.Getenv(envEnabled) != "1" {
		return func(context.Context) error { return nil }
	}
	endpoint := os.Getenv(envEndpoint)
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	t := &tracer{ratio: samplerRatio(os.Getenv(envSampler), os.Getenv(envSamplerArg))}
	t.exporter = newExporter(endpoint, resourceAttributes())
	if !active.CompareAndSwap(nil, t) {
		t.exporter.close()
		return func(context.Context) error { return nil }
	}
	go t.exporter.run()
	return func(ctx context.Context) error {
		active.CompareAndSwap(t, nil)
		return t.exporter.shutdown(ctx)
	}
}

// Enabled reports whether Start turned tracing on.
func Enabled() bool { return active.Load() != nil }

// samplerRatio maps the OTEL_TRACES_SAMPLER guest-init stamps to a sampling
// ratio. The parent's sampled flag is ignored on purpose: the gateway samples
// its own span for platform tracing, which must not hide the app's spans.
func samplerRatio(sampler, arg string) float64 {
	switch sampler {
	case "always_off", "parentbased_always_off":
		return 0
	case "traceidratio", "parentbased_traceidratio":
		ratio, err := strconv.ParseFloat(arg, 64)
		if err != nil || ratio < 0 {
			return 1
		}
		return min(ratio, 1)
	}
	return 1
}

func resourceAttributes() []attribute {
	name := os.Getenv(envServiceName)
	if name == "" {
		name = filepath.Base(os.Args[0])
	}
	return []attribute{
		stringAttr("service.name", name),
		stringAttr("telemetry.sdk.name", scopeName),
		stringAttr("telemetry.sdk.language", "go"),
	}
}

// Kind is a span's role, with the OTLP enum values.
type Kind int

const (
	KindInternal Kind = 1
	KindServer   Kind = 2
	KindClient   Kind = 3
)

type traceID [16]byte
type spanID [8]byte

// spanContext identifies a span; remote is true for one parsed from an
// inbound traceparent header.
type spanContext struct {
	trace   traceID
	span    spanID
	sampled bool
	remote  bool
}

func (c spanContext) valid() bool { return c.trace != (traceID{}) && c.span != (spanID{}) }

// Span is one timed operation. A nil Span is valid and does nothing, which is
// what StartSpan returns while tracing is off.
type Span struct {
	tracer *tracer
	ctx    spanContext
	parent spanID
	kind   Kind
	start  time.Time

	mu        sync.Mutex
	name      string
	attrs     []attribute
	errored   bool
	statusMsg string
	end       time.Time
	ended     bool
}

type spanKey struct{}

// StartSpan starts an internal span under the one in ctx and returns a
// context carrying it. End the span when the operation finishes.
func StartSpan(ctx context.Context, name string) (context.Context, *Span) {
	t := active.Load()
	if t == nil {
		return ctx, nil
	}
	return t.start(ctx, name, KindInternal, spanContext{})
}

// start begins a span under the span in ctx, or under remote when ctx has
// none, or as a new trace's root.
func (t *tracer) start(ctx context.Context, name string, kind Kind, remote spanContext) (context.Context, *Span) {
	s := &Span{tracer: t, kind: kind, name: name, start: time.Now()}
	switch parent := spanFrom(ctx); {
	case parent != nil:
		s.ctx = spanContext{trace: parent.ctx.trace, sampled: parent.ctx.sampled}
		s.parent = parent.ctx.span
	case remote.valid():
		s.ctx = spanContext{trace: remote.trace, sampled: t.sample(remote.trace)}
		s.parent = remote.span
	default:
		s.ctx.trace = newTraceID()
		s.ctx.sampled = t.sample(s.ctx.trace)
	}
	s.ctx.span = newSpanID()
	return context.WithValue(ctx, spanKey{}, s), s
}

// sample decides like OpenTelemetry's TraceIDRatioBased sampler, so every
// span of a trace gets the same decision.
func (t *tracer) sample(id traceID) bool {
	if t.ratio >= 1 {
		return true
	}
	if t.ratio <= 0 {
		return false
	}
	return binary.BigEndian.Uint64(id[8:])>>1 < uint64(t.ratio*(1<<63))
}

func spanFrom(ctx context.Context) *Span {
	s, _ := ctx.Value(spanKey{}).(*Span)
	return s
}

// SetAttribute records a string attribute on the span.
func (s *Span) SetAttribute(key, value string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.attrs = append(s.attrs, stringAttr(key, value))
	s.mu.Unlock()
}

func (s *Span) setInt(key string, value int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.attrs = append(s.attrs, intAttr(key, value))
	s.mu.Unlock()
}

func (s *Span) setName(name string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.name = name
	s.mu.Unlock()
}

// RecordError marks the span failed with err's message.
func (s *Span) RecordError(err error) {
	if s == nil || err == nil {
		return
	}
	s.mu.Lock()
	s.errored, s.statusMsg = true, err.Error()
	s.mu.Unlock()
}

func (s *Span) markError(message string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.errored, s.statusMsg = true, message
	s.mu.Unlock()
}

// End finishes the span and queues it for export. Later calls do nothing.
func (s *Span) End() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended, s.end = true, time.Now()
	s.mu.Unlock()
	if s.ctx.sampled {
		s.tracer.exporter.enqueue(s)
	}
}

// traceparent renders the W3C header that continues this span's trace.
func (s *Span) traceparent() string {
	flags := "00"
	if s.ctx.sampled {
		flags = "01"
	}
	return "00-" + hex.EncodeToString(s.ctx.trace[:]) + "-" + hex.EncodeToString(s.ctx.span[:]) + "-" + flags
}

// parseTraceparent reads a W3C traceparent header (version 00 layout; later
// versions share its prefix).
func parseTraceparent(header string) spanContext {
	parts := strings.Split(strings.TrimSpace(header), "-")
	if len(parts) < 4 || len(parts[0]) != 2 || parts[0] == "ff" || len(parts[1]) != 32 || len(parts[2]) != 16 || len(parts[3]) != 2 {
		return spanContext{}
	}
	var c spanContext
	if _, err := hex.Decode(c.trace[:], []byte(parts[1])); err != nil {
		return spanContext{}
	}
	if _, err := hex.Decode(c.span[:], []byte(parts[2])); err != nil {
		return spanContext{}
	}
	flags, err := hex.DecodeString(parts[3])
	if err != nil {
		return spanContext{}
	}
	c.sampled, c.remote = flags[0]&1 == 1, true
	if !c.valid() {
		return spanContext{}
	}
	return c
}

// IDs come from crypto/rand: a math/rand state captured in a snapshot would
// repeat across instances restored from it.
func newTraceID() traceID {
	var id traceID
	for id == (traceID{}) {
		_, _ = rand.Read(id[:])
	}
	return id
}

func newSpanID() spanID {
	var id spanID
	for id == (spanID{}) {
		_, _ = rand.Read(id[:])
	}
	return id
}

type attribute struct {
	key   string
	str   string
	num   int64
	isNum bool
}

func stringAttr(key, value string) attribute { return attribute{key: key, str: value} }
func intAttr(key string, value int64) attribute {
	return attribute{key: key, num: value, isNum: true}
}
