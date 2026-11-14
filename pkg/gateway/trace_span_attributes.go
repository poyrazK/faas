package gateway

import (
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
	pkgtrace "github.com/onebox-faas/faas/pkg/trace"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// spanAttributes collects a span's attributes and sets them in one call just
// before the span ends. A recording SDK span grows its attribute slice on
// every SetAttributes call: the request span's eight calls, and the temporary
// slices built for them, were gatewayd-internal's second-largest allocation
// site per request (2026-11-06 allocs profile, about 15 KB a request). One
// call grows the span's slice once.
//
// Attributes are set in the order they were added, so a key added twice keeps
// its last value, as with separate calls. A span that is not recording
// collects nothing: collectSpanAttributes returns nil and every method on a
// nil collector is a no-op.
type spanAttributes struct {
	span oteltrace.Span
	kv   []attribute.KeyValue
}

var spanAttributesPool = sync.Pool{New: func() any {
	return &spanAttributes{kv: make([]attribute.KeyValue, 0, 64)}
}}

func collectSpanAttributes(span oteltrace.Span) *spanAttributes {
	if !span.IsRecording() {
		return nil
	}
	collected := spanAttributesPool.Get().(*spanAttributes)
	collected.span = span
	return collected
}

func (a *spanAttributes) add(kv ...attribute.KeyValue) {
	if a != nil {
		a.kv = append(a.kv, kv...)
	}
}

func (a *spanAttributes) addIdentity(identity api.PlatformIdentity) {
	if a != nil {
		a.kv = pkgtrace.AppendPlatformIdentityAttributes(a.kv, identity)
	}
}

func (a *spanAttributes) addTarget(target Target) {
	if a != nil {
		a.kv = appendCompletionTargetAttributes(a.kv, target)
	}
}

func (a *spanAttributes) addDecision(decision trafficDecisionSnapshot) {
	if a != nil {
		a.kv = decision.appendAttributes(a.kv)
	}
}

// flush sets the collected attributes on the span. Call it once, before the
// span ends; the collector must not be used afterwards.
func (a *spanAttributes) flush() {
	if a == nil {
		return
	}
	if len(a.kv) > 0 {
		a.span.SetAttributes(a.kv...)
	}
	// The SDK span copies the values into its own slice, so the buffer can be
	// reused. Another span implementation may keep it: leave that to the GC.
	_, copied := a.span.(sdktrace.ReadWriteSpan)
	a.span = nil
	if !copied {
		return
	}
	clear(a.kv)
	a.kv = a.kv[:0]
	spanAttributesPool.Put(a)
}
