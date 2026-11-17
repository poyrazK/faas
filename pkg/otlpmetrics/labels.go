package otlpmetrics

import (
	"fmt"
	"math"
	"strconv"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"

	"github.com/onebox-faas/faas/pkg/api"
)

// Labels converts a data point's attributes into a custom metric label set
// (ADR-20261108160000 slice C) and its canonical series key: "k=v,..." with
// keys sorted and separators escaped. No attributes is the unlabelled series
// (nil, ""). Keys are normalised like metric names; values must be scalars
// (string, bool, int or double) of at most api.CustomMetricLabelValueMaxBytes
// bytes, and a point may carry at most api.CustomMetricMaxLabels keys.
func Labels(attrs []*commonpb.KeyValue) (map[string]string, string, error) {
	if len(attrs) == 0 {
		return nil, "", nil
	}
	if len(attrs) > api.CustomMetricMaxLabels {
		return nil, "", fmt.Errorf("at most %d attributes per data point are supported", api.CustomMetricMaxLabels)
	}
	labels := make(map[string]string, len(attrs))
	for _, kv := range attrs {
		key := NormalizeName(kv.GetKey())
		if !api.ValidCustomMetricLabelKey(key) {
			return nil, "", fmt.Errorf("attribute %q must normalise to [a-z_][a-z0-9_]{0,62}", kv.GetKey())
		}
		if _, dup := labels[key]; dup {
			return nil, "", fmt.Errorf("attributes normalise to the same key %q", key)
		}
		value, ok := scalarString(kv.GetValue())
		if !ok {
			return nil, "", fmt.Errorf("attribute %q must be a string, bool, int or double", kv.GetKey())
		}
		if len(value) > api.CustomMetricLabelValueMaxBytes {
			return nil, "", fmt.Errorf("attribute %q is longer than %d bytes", kv.GetKey(), api.CustomMetricLabelValueMaxBytes)
		}
		labels[key] = value
	}
	return labels, SeriesKey(labels), nil
}

// SeriesKey is the canonical key of a label set; "" for none.
func SeriesKey(labels map[string]string) string {
	return api.CustomMetricSeriesKey(labels)
}

func scalarString(v *commonpb.AnyValue) (string, bool) {
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return x.StringValue, true
	case *commonpb.AnyValue_BoolValue:
		return strconv.FormatBool(x.BoolValue), true
	case *commonpb.AnyValue_IntValue:
		return strconv.FormatInt(x.IntValue, 10), true
	case *commonpb.AnyValue_DoubleValue:
		if math.IsNaN(x.DoubleValue) || math.IsInf(x.DoubleValue, 0) {
			return "", false
		}
		return strconv.FormatFloat(x.DoubleValue, 'g', -1, 64), true
	}
	return "", false
}
