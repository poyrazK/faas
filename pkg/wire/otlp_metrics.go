package wire

// otlp_metrics.go provides the small push bridge used by daemon-owned
// Prometheus registries. Prometheus remains the source of truth for local
// scraping; this bridge serializes the same gathered families as OTLP/HTTP
// when an operator configures an OTLP endpoint.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	collector "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const defaultOTLPMetricsInterval = time.Minute

// StartOTLPMetrics starts a best-effort Prometheus-to-OTLP/HTTP exporter.
// The returned shutdown function stops the periodic loop and performs one
// bounded final export. Collector availability never gates daemon startup;
// export failures are logged while local Prometheus scraping continues.
func StartOTLPMetrics(
	ctx context.Context,
	gatherer prometheus.Gatherer,
	endpoint string,
	serviceName string,
	log *slog.Logger,
) (func(context.Context) error, error) {
	if endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}
	if gatherer == nil {
		return nil, errors.New("otlp metrics: nil prometheus gatherer")
	}
	if ctx == nil {
		return nil, errors.New("otlp metrics: nil context")
	}
	target, err := normalizeOTLPMetricsEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	if serviceName == "" {
		serviceName = "faas"
	}
	if log == nil {
		log = slog.Default()
	}
	interval := otlpMetricsInterval()
	client := &http.Client{Timeout: 5 * time.Second}
	headers := otlpHeaders()
	bridgeStart := uint64(time.Now().UnixNano())
	stop := make(chan struct{})
	done := make(chan struct{})

	export := func(exportCtx context.Context) error {
		families, gatherErr := gatherer.Gather()
		if gatherErr != nil {
			return fmt.Errorf("gather prometheus metrics: %w", gatherErr)
		}
		payload := prometheusFamiliesToOTLP(families, serviceName, Version, bridgeStart, time.Now())
		body, marshalErr := proto.Marshal(payload)
		if marshalErr != nil {
			return fmt.Errorf("marshal OTLP metrics: %w", marshalErr)
		}
		req, requestErr := http.NewRequestWithContext(exportCtx, http.MethodPost, target, bytes.NewReader(body))
		if requestErr != nil {
			return fmt.Errorf("build OTLP metrics request: %w", requestErr)
		}
		req.Header.Set("Content-Type", "application/x-protobuf")
		req.Header.Set("User-Agent", "faas-otlp-metrics")
		for key, values := range headers {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
		resp, doErr := client.Do(req)
		if doErr != nil {
			return fmt.Errorf("send OTLP metrics: %w", doErr)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			message, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
			return fmt.Errorf("OTLP metrics endpoint returned %s: %s", resp.Status, strings.TrimSpace(string(message)))
		}
		return nil
	}

	log.Info("otlp metrics bridge enabled", "endpoint", target, "interval", interval, "service", serviceName)
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				exportCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				if exportErr := export(exportCtx); exportErr != nil {
					log.Warn("otlp metrics export failed", "err", exportErr)
				}
				cancel()
			}
		}
	}()

	var shutdownOnce sync.Once
	var shutdownErr error
	shutdown := func(shutdownCtx context.Context) error {
		shutdownOnce.Do(func() {
			close(stop)
			<-done
			if shutdownCtx == nil {
				shutdownCtx = context.Background()
			}
			shutdownErr = export(shutdownCtx)
		})
		return shutdownErr
	}
	return shutdown, nil
}

func normalizeOTLPMetricsEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("otlp metrics: empty endpoint")
	}
	parseTarget := raw
	if !strings.Contains(raw, "://") {
		parseTarget = "http://" + raw
	}
	u, err := url.Parse(parseTarget)
	if err != nil {
		return "", fmt.Errorf("otlp metrics: parse endpoint: %w", err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("otlp metrics: endpoint %q has no host", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("otlp metrics: endpoint scheme %q must be http or https", u.Scheme)
	}
	switch {
	case u.Path == "", u.Path == "/":
		u.Path = "/v1/metrics"
	case strings.HasSuffix(u.Path, "/v1/traces"):
		u.Path = strings.TrimSuffix(u.Path, "/v1/traces") + "/v1/metrics"
	case !strings.HasSuffix(u.Path, "/v1/metrics"):
		u.Path = strings.TrimRight(u.Path, "/") + "/v1/metrics"
	}
	return u.String(), nil
}

func otlpMetricsInterval() time.Duration {
	value := strings.TrimSpace(os.Getenv("OTEL_METRIC_EXPORT_INTERVAL"))
	if value == "" {
		return defaultOTLPMetricsInterval
	}
	if milliseconds, err := strconv.ParseInt(value, 10, 64); err == nil && milliseconds > 0 {
		return time.Duration(milliseconds) * time.Millisecond
	}
	if duration, err := time.ParseDuration(value); err == nil && duration > 0 {
		return duration
	}
	return defaultOTLPMetricsInterval
}

func otlpHeaders() http.Header {
	raw := os.Getenv("OTEL_EXPORTER_OTLP_METRICS_HEADERS")
	if raw == "" {
		raw = os.Getenv("OTEL_EXPORTER_OTLP_HEADERS")
	}
	headers := make(http.Header)
	for _, pair := range strings.Split(raw, ",") {
		key, value, ok := strings.Cut(pair, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			continue
		}
		if decoded, err := url.QueryUnescape(strings.TrimSpace(value)); err == nil {
			value = decoded
		}
		headers.Set(key, value)
	}
	return headers
}

func prometheusFamiliesToOTLP(
	families []*dto.MetricFamily,
	serviceName, serviceVersion string,
	bridgeStart uint64,
	now time.Time,
) *collector.ExportMetricsServiceRequest {
	timestamp := uint64(now.UnixNano())
	metrics := make([]*metricspb.Metric, 0, len(families))
	for _, family := range families {
		if family == nil || family.GetName() == "" {
			continue
		}
		metric := &metricspb.Metric{
			Name:        family.GetName(),
			Description: family.GetHelp(),
			Unit:        family.GetUnit(),
		}
		switch family.GetType() {
		case dto.MetricType_COUNTER:
			points := make([]*metricspb.NumberDataPoint, 0, len(family.GetMetric()))
			for _, sample := range family.GetMetric() {
				if sample == nil {
					continue
				}
				pointTime := prometheusMetricTimestamp(sample, timestamp)
				start := prometheusStartTimestamp(sample.GetCounter().GetCreatedTimestamp(), bridgeStart)
				points = append(points, numberDataPoint(sample.GetLabel(), sample.GetCounter().GetValue(), pointTime, start))
			}
			metric.Data = &metricspb.Metric_Sum{Sum: &metricspb.Sum{
				DataPoints:             points,
				AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE,
				IsMonotonic:            true,
			}}
		case dto.MetricType_HISTOGRAM, dto.MetricType_GAUGE_HISTOGRAM:
			points := make([]*metricspb.HistogramDataPoint, 0, len(family.GetMetric()))
			for _, sample := range family.GetMetric() {
				if sample == nil || sample.GetHistogram() == nil {
					continue
				}
				histogram := sample.GetHistogram()
				pointTime := prometheusMetricTimestamp(sample, timestamp)
				start := prometheusStartTimestamp(histogram.GetCreatedTimestamp(), bridgeStart)
				points = append(points, histogramDataPoint(sample.GetLabel(), histogram, pointTime, start))
			}
			metric.Data = &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{
				DataPoints:             points,
				AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE,
			}}
		case dto.MetricType_SUMMARY:
			points := make([]*metricspb.SummaryDataPoint, 0, len(family.GetMetric()))
			for _, sample := range family.GetMetric() {
				if sample == nil || sample.GetSummary() == nil {
					continue
				}
				summary := sample.GetSummary()
				pointTime := prometheusMetricTimestamp(sample, timestamp)
				start := prometheusStartTimestamp(summary.GetCreatedTimestamp(), bridgeStart)
				points = append(points, summaryDataPoint(sample.GetLabel(), summary, pointTime, start))
			}
			metric.Data = &metricspb.Metric_Summary{Summary: &metricspb.Summary{DataPoints: points}}
		case dto.MetricType_GAUGE, dto.MetricType_UNTYPED:
			points := make([]*metricspb.NumberDataPoint, 0, len(family.GetMetric()))
			for _, sample := range family.GetMetric() {
				if sample == nil {
					continue
				}
				value := sample.GetGauge().GetValue()
				if sample.GetGauge() == nil && sample.GetUntyped() != nil {
					value = sample.GetUntyped().GetValue()
				}
				points = append(points, numberDataPoint(sample.GetLabel(), value, prometheusMetricTimestamp(sample, timestamp), 0))
			}
			metric.Data = &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: points}}
		default:
			continue
		}
		metrics = append(metrics, metric)
	}
	return &collector.ExportMetricsServiceRequest{ResourceMetrics: []*metricspb.ResourceMetrics{{
		Resource: &resourcepb.Resource{Attributes: resourceAttributes(serviceName, serviceVersion)},
		ScopeMetrics: []*metricspb.ScopeMetrics{{
			Scope:   &commonpb.InstrumentationScope{Name: "faas.prometheus.bridge", Version: serviceVersion},
			Metrics: metrics,
		}},
	}}}
}

func resourceAttributes(serviceName, serviceVersion string) []*commonpb.KeyValue {
	attrs := []*commonpb.KeyValue{stringAttribute("service.name", serviceName)}
	if serviceVersion != "" {
		attrs = append(attrs, stringAttribute("service.version", serviceVersion))
	}
	return attrs
}

func stringAttribute(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{
		Key:   key,
		Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}},
	}
}

func prometheusMetricTimestamp(sample *dto.Metric, fallback uint64) uint64 {
	if sample != nil && sample.GetTimestampMs() > 0 {
		return uint64(sample.GetTimestampMs()) * uint64(time.Millisecond)
	}
	return fallback
}

func prometheusStartTimestamp(created *timestamppb.Timestamp, fallback uint64) uint64 {
	if created != nil {
		if nanos := created.AsTime().UnixNano(); nanos > 0 {
			return uint64(nanos)
		}
	}
	return fallback
}

func numberDataPoint(labels []*dto.LabelPair, value float64, timestamp, start uint64) *metricspb.NumberDataPoint {
	return &metricspb.NumberDataPoint{
		Attributes:        prometheusAttributes(labels),
		StartTimeUnixNano: start,
		TimeUnixNano:      timestamp,
		Value:             &metricspb.NumberDataPoint_AsDouble{AsDouble: value},
	}
}

func histogramDataPoint(labels []*dto.LabelPair, histogram *dto.Histogram, timestamp, start uint64) *metricspb.HistogramDataPoint {
	bounds := make([]float64, 0, len(histogram.GetBucket()))
	counts := make([]uint64, 0, len(histogram.GetBucket())+1)
	var previous uint64
	for _, bucket := range histogram.GetBucket() {
		if bucket == nil {
			continue
		}
		cumulative := bucket.GetCumulativeCount()
		count := cumulative - previous
		previous = cumulative
		if math.IsInf(bucket.GetUpperBound(), 1) {
			counts = append(counts, count)
			continue
		}
		bounds = append(bounds, bucket.GetUpperBound())
		counts = append(counts, count)
	}
	if len(counts) == len(bounds) {
		counts = append(counts, histogram.GetSampleCount()-previous)
	}
	sum := histogram.GetSampleSum()
	return &metricspb.HistogramDataPoint{
		Attributes:        prometheusAttributes(labels),
		StartTimeUnixNano: start,
		TimeUnixNano:      timestamp,
		Count:             histogram.GetSampleCount(),
		Sum:               &sum,
		BucketCounts:      counts,
		ExplicitBounds:    bounds,
	}
}

func summaryDataPoint(labels []*dto.LabelPair, summary *dto.Summary, timestamp, start uint64) *metricspb.SummaryDataPoint {
	quantiles := make([]*metricspb.SummaryDataPoint_ValueAtQuantile, 0, len(summary.GetQuantile()))
	for _, quantile := range summary.GetQuantile() {
		if quantile == nil {
			continue
		}
		quantiles = append(quantiles, &metricspb.SummaryDataPoint_ValueAtQuantile{
			Quantile: quantile.GetQuantile(),
			Value:    quantile.GetValue(),
		})
	}
	return &metricspb.SummaryDataPoint{
		Attributes:        prometheusAttributes(labels),
		StartTimeUnixNano: start,
		TimeUnixNano:      timestamp,
		Count:             summary.GetSampleCount(),
		Sum:               summary.GetSampleSum(),
		QuantileValues:    quantiles,
	}
}

func prometheusAttributes(labels []*dto.LabelPair) []*commonpb.KeyValue {
	attrs := make([]*commonpb.KeyValue, 0, len(labels))
	for _, label := range labels {
		if label == nil || label.GetName() == "" {
			continue
		}
		attrs = append(attrs, stringAttribute(label.GetName(), label.GetValue()))
	}
	return attrs
}
