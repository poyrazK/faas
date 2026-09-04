// Package gateway — trace_setup.go (issue #555 PR-2). Wires the
// TraceRing + TraceRingExporter + TraceHandler into a unified
// TracerProvider that the caller installs via otel.SetTracerProvider.
//
// Why this lives in pkg/gateway (not pkg/wire/otelinit): the
// TraceRing is gateway-specific state. pkg/wire/otelinit owns the
// SDK boot; pkg/gateway/trace_setup owns the application wiring.
//
// The split mirrors the existing pkg/gateway package boundary: the
// routes-RingCache is gateway state, the metrics registry is wire
// state. The OTel SDK is the cross-cutting concern; the ring is
// the gateway-specific consumer.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"github.com/onebox-faas/faas/pkg/wire/otelinit"
)

// envTraceRingCap is the FAAS-flavored env var for the ring cap.
// Defaults to DefaultTraceRingCap when unset.
const envTraceRingCap = "FAAS_TRACE_RING_CAP"

// gatewayDefaultSamplingRate is the head sampler rate when
// OTEL_TRACES_SAMPLER_ARG is unset. 1.0 matches the otelinit
// default so a gatewayd-public wired through this function
// samples at the same rate as schedd's wire/otelinit boot path.
const gatewayDefaultSamplingRate = 1.0

// envObserverToken is the env var for the X-Faas-Trace-Auth header
// gate. Empty value disables the GET endpoint (returns 404).
//
// Security note (issue #555 review): this token is the sole trust
// boundary on GET /v1/traces/{trace_id}. With a valid token the
// holder can read any trace in the last 24h — across all accounts,
// across all apps. Treat FAAS_TRACE_OBSERVER_TOKEN with the same
// care as a database superuser password: distribute via systemd
// LoadCredential (see §11) and rotate via the same cadence as the
// dashboard session signing key. An empty value is the safe
// default; the handler returns 404 in that case so a missing
// config does not silently disable auth.
const envObserverToken = "FAAS_TRACE_OBSERVER_TOKEN"

// TraceSetup is the runtime bundle the daemon installs at boot.
type TraceSetup struct {
	// Ring is the populated ring. The daemon mounts the handler
	// on the public mux.
	Ring *TraceRing
	// Handler is the http.Handler for GET /v1/traces/{trace_id}.
	Handler http.Handler
	// Shutdown is the OTLP/SDK shutdown func. The daemon calls it
	// after the listeners drain so any in-flight spans are flushed.
	Shutdown func(ctx context.Context) error
	// TracerProvider is the installed OTel TracerProvider. The
	// daemon hands it to the otelhttp.NewHandler / otelgrpc
	// interceptors (PR-3).
	TracerProvider *sdktrace.TracerProvider
	// DeploymentCounter is the per-deployment 100% sampling
	// window counter the gatewayd-internal-side sampler consults (issue
	// #555 / ADR-055). schedd's pkg/sched/deployment_counter_watcher
	// watches the in-process Platform wake topic and calls
	// counter.Reset(deploymentID) on the "last live instance
	// parked" transition — the watcher wires against THIS counter
	// (not the otelinit-owned one in schedd) so a reset on either
	// daemon collapses into the same semantic: the next wake for
	// that deployment gets a fresh 100-span window.
	//
	// Nil when the OTLP endpoint is unset AND the operator
	// disabled deployment-aware sampling via env — the watcher
	// nil-checks before calling Reset.
	DeploymentCounter *otelinit.DeploymentCounter
}

// InstallTracePipeline builds a TraceRing + OTLP exporter (if env
// set) + TraceRingExporter + TraceHandler, installs the resulting
// TracerProvider globally, and returns the bundle. The caller is
// responsible for:
//
//  1. Mounting Handler on the public mux (e.g. mux.Handle("/v1/traces/",
//     bundle.Handler)).
//  2. Calling bundle.Shutdown(ctx) after the listeners drain.
//
// `name` is the OTel service.name (e.g. "gatewayd-public").
// `version` is the OTel service.version (typically wire.Version).
func InstallTracePipeline(ctx context.Context, name, version string, log *slog.Logger) (*TraceSetup, error) {
	return InstallTracePipelineWithRegistry(ctx, name, version, log, nil, "")
}

// InstallTracePipelineWithRegistry is InstallTracePipeline with trace
// exporter health metrics attached to reg. metricPrefix should match the
// daemon's wire.OpsMetrics prefix (gatewayd_public for gatewayd-public).
func InstallTracePipelineWithRegistry(ctx context.Context, name, version string, log *slog.Logger, reg prometheus.Registerer, metricPrefix string) (*TraceSetup, error) {
	ring := buildRingFromEnv()

	// W3C TraceContext + Baggage propagators (canonical OTel
	// default). otelgrpc / otelhttp pick these up via
	// otel.GetTextMapPropagator().
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Build the resource attributes (service.name, service.version).
	// WithFromEnv contributes OTEL_RESOURCE_ATTRIBUTES values set
	// by the operator (e.g. deployment.environment=production).
	res, err := sdkresource.New(ctx,
		sdkresource.WithAttributes(
			semconv.ServiceName(name),
			semconv.ServiceVersion(version),
		),
		sdkresource.WithFromEnv(),
	)
	if err != nil {
		return nil, fmt.Errorf("trace_setup: build resource: %w", err)
	}

	// SpanExporter list: the ring exporter is always present; the
	// OTLP exporter is conditional. Both share a single batch via
	// the multi-exporter shape so we don't pay the per-batch
	// overhead twice (issue #555 review: a previous revision
	// mounted the ring exporter twice, doubling the ring's per-
	// span cost).
	exporters := []sdktrace.SpanExporter{NewTraceRingExporter(ring, log)}
	health, err := otelinit.NewTraceExporterHealth(reg, metricPrefix)
	if err != nil {
		return nil, fmt.Errorf("trace_setup: register trace exporter health: %w", err)
	}
	otlpExporter, otlpErr := buildOTLPExporter(ctx, log)
	if otlpErr != nil {
		// The OTLP endpoint is optional. Log a warning and run
		// with the ring-only exporter so the daemon still serves
		// GET /v1/traces/{trace_id} even when the collector is
		// down.
		log.Warn("trace_setup: OTLP exporter disabled", "err", otlpErr)
		if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" {
			health.SetUnavailable()
		}
	}
	if otlpExporter != nil {
		health.SetEnabled(true)
		otlpExporter = health.Wrap(otlpExporter)
	} else if otlpErr == nil {
		health.SetEnabled(false)
	}
	if otlpExporter != nil {
		exporters = append(exporters, otlpExporter)
	}

	sampler, counter := buildSampler(log)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(multiExporter(exporters)),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)
	otel.SetTracerProvider(tp)

	handler := NewTraceHandler(TraceHandlerConfig{
		Ring:          ring,
		ObserverToken: os.Getenv(envObserverToken),
	})

	return &TraceSetup{
		Ring:              ring,
		Handler:           handler,
		TracerProvider:    tp,
		Shutdown:          tp.Shutdown,
		DeploymentCounter: counter,
	}, nil
}

// buildRingFromEnv reads FAAS_TRACE_RING_CAP. Empty or invalid
// values fall back to default.
func buildRingFromEnv() *TraceRing {
	cap := DefaultTraceRingCap
	if v := os.Getenv(envTraceRingCap); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cap = n
		}
	}
	return NewTraceRing(cap)
}

// buildOTLPExporter returns the OTLP/HTTP exporter if
// OTEL_EXPORTER_OTLP_ENDPOINT is set, nil otherwise.
func buildOTLPExporter(ctx context.Context, log *slog.Logger) (sdktrace.SpanExporter, error) {
	raw := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if raw == "" {
		return nil, nil
	}
	endpoint := raw
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		endpoint = u.String()
	}
	log.Info("trace_setup: OTLP exporter enabled", "endpoint", endpoint)
	options := []otlptracehttp.Option{otlptracehttp.WithTimeout(5 * time.Second)}
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		options = append(options, otlptracehttp.WithEndpointURL(endpoint))
		if strings.HasPrefix(endpoint, "http://") {
			options = append(options, otlptracehttp.WithInsecure())
		}
	} else {
		options = append(options,
			otlptracehttp.WithEndpoint(endpoint),
			otlptracehttp.WithInsecure(),
		)
	}
	return otlptracehttp.New(ctx, options...)
}

// multiExporter fans ExportSpans / Shutdown to a list of exporters.
// The SDK doesn't ship a public multi-exporter helper in v1.43; this
// is the canonical short implementation.
//
// Errors are joined with errors.Join so the daemon drain can see
// every sub-exporter failure (issue #555 review: a previous
// revision silently dropped all errors, hiding "OTLP collector
// shutdown timed out" from the operator).
type multiExporter []sdktrace.SpanExporter

func (m multiExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	var errs []error
	for _, e := range m {
		if err := e.ExportSpans(ctx, spans); err != nil {
			// Continue to the next exporter so a single
			// failure does not starve the others.
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m multiExporter) Shutdown(ctx context.Context) error {
	var errs []error
	for _, e := range m {
		if err := e.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// envTracesSamplerArg is the OTel-standard env var for the head
// sampler rate. Parsed identically to otelinit.go:182-190 —
// strconv.ParseFloat with the same "invalid → fall back to
// default + warn" semantics so a misconfigured operator sees the
// same diagnostic regardless of which daemon they look at.
const envTracesSamplerArg = "OTEL_TRACES_SAMPLER_ARG"

// buildSampler constructs the per-deployment-aware sampler
// (issue #555 closure / ADR-055). The sampler chain is:
//
//	sdktrace.ParentBased(
//	    otelinit.NewDeploymentAware(
//	        sdktrace.TraceIDRatioBased(rate),
//	        otelinit.WithCounter(counter),
//	    ),
//	)
//
// ParentBased is the outer wrapper so a child span inherits the
// parent's SampledFlag (W3C TraceContext invariant). DeploymentAware
// only sees root spans and either forces RecordAndSample inside the
// per-deployment window (counter.Observe returns inside=true) or
// delegates to TraceIDRatioBased. The counter is returned to the
// caller so pkg/sched/deployment_counter_watcher.go can wire its
// Reset against the SAME map the sampler consults — without the
// return, the watcher would reset a counter the sampler never reads.
//
// We deliberately do NOT mirror otelinit.Init's no-endpoint noop
// branch (otelinit.go:158-168). The ring exporter is unconditional;
// a gatewayd-public without OTLP must still feed the ring so
// GET /v1/traces/{trace_id} continues to return spans from the
// last 24h. The sampler runs against the SDK regardless of whether
// the OTLP exporter is wired, so a deployment's first 100 root
// spans are still captured into the local ring.
func buildSampler(log *slog.Logger) (sdktrace.Sampler, *otelinit.DeploymentCounter) {
	counter := otelinit.NewDeploymentCounter(otelinit.DefaultWindowSize)
	rate := gatewayDefaultSamplingRate
	if v := os.Getenv(envTracesSamplerArg); v != "" {
		parsed, err := strconv.ParseFloat(v, 64)
		if err != nil {
			// Issue #555 review parity with otelinit.go:188 —
			// fall back to the default rate but log loudly so a
			// misconfigured operator can spot the typo. ParseFloat
			// (vs fmt.Sscanf) rejects trailing junk ("1.0xyz"
			// parsed as 1.0 pre-#555).
			log.Warn("gateway/trace_setup: invalid OTEL_TRACES_SAMPLER_ARG, falling back",
				"value", v, "err", err, "fallback", gatewayDefaultSamplingRate)
		} else {
			rate = parsed
		}
	}
	root := sdktrace.TraceIDRatioBased(rate)
	sampler := sdktrace.ParentBased(otelinit.NewDeploymentAware(root, otelinit.WithCounter(counter)))
	log.Info("gateway/trace_setup: deployment-aware sampler wired",
		"sampler_arg", rate, "window_size", counter.WindowSize())
	return sampler, counter
}
