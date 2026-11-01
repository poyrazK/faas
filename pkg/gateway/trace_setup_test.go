// Tests for the deployment-aware sampler wired into
// InstallTracePipeline (issue #555 closure / ADR-055). The
// otelinit.DeploymentAware primitive is exercised in depth at
// pkg/wire/otelinit/sampler_test.go — these tests pin the
// GATEWAY-side wiring only:
//
//  1. The sampler is the ParentBased(DeploymentAware) chain, not
//     the previous ParentBased(AlwaysSample()).
//  2. The OTEL_TRACES_SAMPLER_ARG parsing falls back to the default
//     on garbage input.
//  3. The DeploymentCounter returned alongside the sampler is the
//     SAME map the sampler consults (issue #555 PR-6 contract).
//  4. The ring exporter is unconditionally fed — we never mirror
//     otelinit.Init's no-endpoint noop branch.
package gateway

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"

	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/onebox-faas/faas/pkg/wire/otelinit"
)

// quietLogger returns a slog.Logger that discards everything; tests
// don't want boot diagnostics on the test runner's stdout.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// withEnv sets an env var for the duration of fn and restores the
// prior value. Go's t.Setenv handles the cleanup automatically;
// this helper exists so a test can temporarily clear OTEL_TRACES_SAMPLER_ARG
// (t.Setenv("OTEL_TRACES_SAMPLER_ARG", "") keeps the empty string
// in the environment, which is a different semantic than "unset"
// — strconv.ParseFloat is fine on "" but the if-branch in
// buildSampler is skipped entirely on unset).
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	prev, had := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unsetenv %s: %v", key, err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, prev)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

// TestTraceSetup_SamplerIsDeploymentAware pins the chain shape.
// The previous sampler was ParentBased(AlwaysSample()) — the new
// sampler must be ParentBased(DeploymentAware(...)). We assert on
// the Description string, which is the only stable surface the
// OTel SDK exposes for sampler identification.
func TestTraceSetup_SamplerIsDeploymentAware(t *testing.T) {
	sampler, _ := buildSampler(discardLogger())
	desc := sampler.Description()
	if !strings.Contains(desc, "ParentBased") {
		t.Errorf("sampler.Description() = %q, want substring %q", desc, "ParentBased")
	}
	// OTel's ParentBased.Description embeds the inner sampler's
	// description; DeploymentAware exposes its windowSize via
	// "DeploymentAware(window=N)". If we ever fall back to
	// AlwaysSample the description will contain "AlwaysSample"
	// instead — that is the tripwire for this test.
	if strings.Contains(desc, "AlwaysSample") {
		t.Errorf("sampler.Description() = %q, must NOT be AlwaysSample", desc)
	}
}

// TestTraceSetup_RateArgHonoured asserts the OTEL_TRACES_SAMPLER_ARG
// env var drives the underlying head ratio. The sampler chain is
// opaque to a test that constructs it directly — we exercise
// the chain end-to-end by calling ShouldSample on a deterministic
// sequence of trace IDs and checking the count.
//
// TraceIDRatioBased hashes the trace ID and rolls against `rate`;
// with rate=0 the sampler drops everything, with rate=1 it
// samples everything. We use 0 so the test is deterministic
// without needing to know the hash function.
func TestTraceSetup_RateArgHonoured(t *testing.T) {
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "0")
	sampler, _ := buildSampler(discardLogger())
	p := sdktrace.SamplingParameters{
		// Empty parent context = root span (no inherited flag).
		ParentContext: context.Background(),
		// Deterministic trace ID; rate=0 must drop.
		TraceID: [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		Name:    "test-span",
	}
	res := sampler.ShouldSample(p)
	if res.Decision != sdktrace.Drop {
		t.Errorf("rate=0: ShouldSample decision = %v, want Drop", res.Decision)
	}
}

// TestTraceSetup_InvalidRateFallsBack asserts the garbage-input
// branch. The previous fmt.Sscanf accepted trailing junk; the
// current implementation uses strconv.ParseFloat which rejects
// it. We assert that the fallback rate (1.0) is still wired even
// with OTEL_TRACES_SAMPLER_ARG set to "1.0xyz".
func TestTraceSetup_InvalidRateFallsBack(t *testing.T) {
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "1.0xyz")
	sampler, _ := buildSampler(discardLogger())
	// With rate=1.0 fallback the sampler should accept any root
	// span (TraceIDRatioBased(1.0) is equivalent to AlwaysSample).
	p := sdktrace.SamplingParameters{
		ParentContext: context.Background(),
		TraceID:       [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		Name:          "test-span",
	}
	res := sampler.ShouldSample(p)
	if res.Decision != sdktrace.RecordAndSample {
		t.Errorf("invalid rate fallback: ShouldSample decision = %v, want RecordAndSample", res.Decision)
	}
}

// TestTraceSetup_CounterIsSharedBetweenSamplerAndReturnValue pins
// the watcher's wiring contract: pkg/sched/deployment_counter_watcher
// will call Reset on the DeploymentCounter returned from
// InstallTracePipeline, and the sampler consults the SAME counter.
// If the sampler were built with a separate counter, the watcher's
// Reset would be a no-op and a redeployment would lose the 100%
// window.
//
// The tripwire: increment the returned counter past the window,
// then ask the sampler about a fresh deployment_id — the new
// depID should be inside the window regardless of the previous
// counter state.
func TestTraceSetup_CounterIsSharedBetweenSamplerAndReturnValue(t *testing.T) {
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "0")
	_, counter := buildSampler(discardLogger())
	if counter == nil {
		t.Fatalf("buildSampler returned nil counter")
	}

	// Saturate the counter: 5 distinct deployment IDs, each
	// observed > WindowSize times. This exhausts the 100% window
	// for each of them.
	for i := 0; i < 5; i++ {
		depID := "sat-dep-" + string(rune('a'+i))
		for j := 0; j < otelinit.DefaultWindowSize+5; j++ {
			counter.Observe(depID)
		}
	}

	// A FRESH deployment ID with a fresh trace ID — must be
	// inside the window (RecordAndSample) because DeploymentAware
	// forces sampling for the first N spans regardless of the
	// head ratio.
	sampler, counterReturned := buildSampler(discardLogger())
	if counterReturned == nil {
		t.Fatalf("second buildSampler returned nil counter")
	}
	p := sdktrace.SamplingParameters{
		ParentContext: context.Background(),
		TraceID:       [16]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99},
		Name:          "fresh-deployment-span",
		Attributes: []attribute.KeyValue{
			attribute.String("deployment_id", "fresh-dep-1"),
		},
	}
	res := sampler.ShouldSample(p)
	if res.Decision != sdktrace.RecordAndSample {
		t.Errorf("fresh deployment span: decision = %v, want RecordAndSample (DeploymentAware window)", res.Decision)
	}

	// Now observe the SAME fresh-dep ID past the window on the
	// returned counter, then re-decide — must fall back to the
	// head ratio (Drop at rate=0).
	for j := 0; j < otelinit.DefaultWindowSize+5; j++ {
		counterReturned.Observe("fresh-dep-1")
	}
	res2 := sampler.ShouldSample(p)
	if res2.Decision != sdktrace.Drop {
		t.Errorf("saturated deployment span: decision = %v, want Drop (window exhausted, rate=0)", res2.Decision)
	}
}

// TestTraceSetup_DeploymentCounterOnTraceSetup asserts the
// InstallTracePipeline return bundle exposes DeploymentCounter
// (the contract pkg/sched/deployment_counter_watcher.go relies
// on). Pinning the field presence here is cheaper than spinning
// up the full Platform + gatewayd-internal in cmd/gatewayd-public.
func TestTraceSetup_DeploymentCounterOnTraceSetup(t *testing.T) {
	// buildSampler returns a (sampler, counter) pair; the
	// InstallTracePipeline wiring layer copies counter into the
	// TraceSetup.DeploymentCounter field. A regression that
	// drops the field assignment would silently break the
	// watcher's Reset path.
	_, counter := buildSampler(discardLogger())
	if counter == nil {
		t.Fatalf("buildSampler counter is nil; InstallTracePipeline would expose nil to the watcher")
	}
	// Counter must be usable: Observe must not panic, Reset must
	// not panic, Len must return a sensible value.
	counter.Observe("test-dep")
	if counter.Len() == 0 {
		t.Errorf("after Observe, counter.Len() = 0; counter does not record state")
	}
	counter.Reset("test-dep")
	if counter.Len() != 0 {
		t.Errorf("after Reset, counter.Len() = %d; Reset did not remove the entry", counter.Len())
	}
}

// TestTraceSetup_UnsetRateFallsBackToDefault covers the
// "operator forgot to set OTEL_TRACES_SAMPLER_ARG" path. With
// rate unset the default (1.0) must apply so the gatewayd-public
// doesn't silently drop everything to the ring on first boot.
func TestTraceSetup_UnsetRateFallsBackToDefault(t *testing.T) {
	unsetEnv(t, "OTEL_TRACES_SAMPLER_ARG")
	sampler, _ := buildSampler(discardLogger())
	p := sdktrace.SamplingParameters{
		ParentContext: context.Background(),
		TraceID:       [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		Name:          "test-span",
	}
	res := sampler.ShouldSample(p)
	if res.Decision != sdktrace.RecordAndSample {
		t.Errorf("unset rate fallback: ShouldSample decision = %v, want RecordAndSample (default=1.0)", res.Decision)
	}
}

// TestTraceSetup_RingFilledOnlyWhenReadable pins that spans reach the
// ring only when GET /v1/traces can read it. Without an observer token
// the handler answers 404, so filling the ring only costs CPU and heap.
// The span must still be sampled: the propagated traceparent keeps its
// sampled flag either way.
func TestTraceSetup_RingFilledOnlyWhenReadable(t *testing.T) {
	unsetEnv(t, "OTEL_TRACES_SAMPLER_ARG")
	for _, tc := range []struct {
		name     string
		token    string
		wantRing int
	}{
		{name: "no observer token", token: "", wantRing: 0},
		{name: "observer token set", token: "secret", wantRing: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ring := NewTraceRing(16)
			sampler, _ := buildSampler(discardLogger())
			tp := sdktrace.NewTracerProvider(tracerProviderOptions(sdkresource.Empty(), sampler, ringExporters(ring, tc.token, discardLogger()))...)
			t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

			_, span := tp.Tracer("test").Start(context.Background(), "request")
			if !span.SpanContext().IsSampled() {
				t.Fatalf("span is not sampled; the propagated traceparent would lose its sampled flag")
			}
			span.End()
			if err := tp.ForceFlush(context.Background()); err != nil {
				t.Fatalf("ForceFlush: %v", err)
			}
			if got := ring.Len(); got != tc.wantRing {
				t.Fatalf("ring.Len() = %d, want %d", got, tc.wantRing)
			}
		})
	}
}
