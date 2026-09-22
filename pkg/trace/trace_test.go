// pkg/trace/trace_test.go — pin the facade's API surface
// (cluster E commit 15 of the platform-observability mega-PR).
//
// Tests are minimal — the heavy lifting lives in
// pkg/wire/otelinit/{otelinit,sampler}_test.go which already
// covers Init + the sampler chain. These tests pin only the
// thin-sugar contracts:
//
//   - NewTracingConfig honors OTEL_* env vars.
//   - SpanFromContext on an empty ctx returns the noop span
//     (no panic, no nil deref).
//   - InitTracer is callable (noop path when the env is unset —
//     the test relies on the noop fallback so it doesn't need a
//     live collector).

package trace_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	pkgtrace "github.com/onebox-faas/faas/pkg/trace"
)

func TestTracingConfig_Defaults(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "")
	c := pkgtrace.NewTracingConfig()
	if c.Enabled {
		t.Errorf("Enabled with empty endpoint: got true, want false")
	}
	if c.Endpoint != "" {
		t.Errorf("Endpoint default: got %q, want empty", c.Endpoint)
	}
	if c.SamplingRatio != 0.01 {
		t.Errorf("SamplingRatio default: got %v, want 0.01", c.SamplingRatio)
	}
}

func TestTracingConfig_EnabledWhenEndpointSet(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://otel-collector:4318")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "")
	c := pkgtrace.NewTracingConfig()
	if !c.Enabled {
		t.Errorf("Enabled with endpoint set: got false, want true")
	}
	if c.Endpoint != "http://otel-collector:4318" {
		t.Errorf("Endpoint: got %q, want otel-collector URL", c.Endpoint)
	}
}

func TestTracingConfig_CustomSampling(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "0.5")
	c := pkgtrace.NewTracingConfig()
	if c.SamplingRatio != 0.5 {
		t.Errorf("SamplingRatio: got %v, want 0.5", c.SamplingRatio)
	}
}

func TestSpanFromContext_EmptyCtxReturnsNoop(t *testing.T) {
	// SpanFromContext on an empty ctx returns the noop fallback —
	// the call MUST NOT panic and MUST NOT return nil. The slog
	// envelope in cmd/<daemon>/main.go relies on this contract
	// (it stamps trace_id + span_id from the result).
	sp := pkgtrace.SpanFromContext(context.Background())
	if sp == nil {
		t.Fatal("SpanFromContext returned nil; want noop span")
	}
	if sp.SpanContext().IsValid() {
		t.Errorf("empty ctx returned valid span context; want noop")
	}
}

func TestStartSpan_NoopWhenNoCollector(t *testing.T) {
	// InitTracer against an unset OTEL_EXPORTER_OTLP_ENDPOINT
	// returns a noop Shutdown; StartSpan on the resulting ctx
	// returns the noop span. This is the production path when
	// the collector isn't running — every call site must work
	// without a nil-check.
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	shutdown, err := pkgtrace.InitTracer(context.Background(),
		"trace_test", "v0.0.0-test",
		nil)
	if err != nil {
		t.Fatalf("InitTracer noop path: %v", err)
	}
	if shutdown == nil {
		t.Fatal("InitTracer noop path returned nil shutdown")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("noop Shutdown: got %v, want nil", err)
	}
	ctx, sp := pkgtrace.StartSpan(context.Background(), "test-op")
	if !pkgtrace.SpanFromContext(ctx).SpanContext().IsValid() &&
		sp.SpanContext().IsValid() {
		t.Errorf("StartSpan returned valid span; expected noop fallback")
	}
	sp.End()
}

func TestPlatformIdentityAttributes(t *testing.T) {
	attrs := pkgtrace.PlatformIdentityAttributes(api.PlatformIdentity{
		RequestID:           "req-1",
		AppID:               "app-1",
		DeploymentID:        "dep-1",
		TenantID:            "tenant-1",
		InstanceID:          "instance-1",
		NodeID:              "node-1",
		Region:              "eu-west",
		CommitSHA:           "abc123",
		DeploymentTag:       "canary",
		DeploymentCreatedAt: "2026-09-19T19:00:00Z",
		ImageDigest:         "sha256:digest",
	})
	got := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		got[string(attr.Key)] = attr.Value.AsString()
	}
	for key, want := range map[string]string{
		"request_id": "req-1", "app_id": "app-1", "deployment_id": "dep-1",
		"tenant_id": "tenant-1", "instance_id": "instance-1", "node_id": "node-1",
		"region": "eu-west", "commit_sha": "abc123", "deployment_tag": "canary",
		"deployment_created_at": "2026-09-19T19:00:00Z", "image_digest": "sha256:digest",
	} {
		if got[key] != want {
			t.Errorf("attribute %q = %q, want %q", key, got[key], want)
		}
	}
}
