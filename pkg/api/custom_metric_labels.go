package api

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Custom metric labels (ADR-20261108160000 slice C). A labelled metric's
// series is named by its canonical key: "k=v,..." with keys sorted and '%',
// ',' and '=' percent-escaped in values. Ingestion (pkg/otlpmetrics), the
// store, the exporter's labels label and every label filter (alert rules,
// release gates, scaling targets) use this one function, so a filter
// selects exactly the series ingestion wrote.

// customMetricLabelKeyPattern is a normalised label key: a Prometheus-safe
// identifier, the shape of a metric name but allowed to start with '_'.
var customMetricLabelKeyPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

var customMetricSeriesKeyEscaper = strings.NewReplacer("%", "%25", ",", "%2C", "=", "%3D")

// CustomMetricSeriesKey is the canonical key of a label set; "" for none.
func CustomMetricSeriesKey(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + customMetricSeriesKeyEscaper.Replace(labels[k])
	}
	return strings.Join(parts, ",")
}

// ValidCustomMetricLabelKey reports whether key is a normalised label key.
func ValidCustomMetricLabelKey(key string) bool {
	return customMetricLabelKeyPattern.MatchString(key)
}

// ValidateCustomMetricLabels checks a label filter: at most
// CustomMetricMaxLabels keys of the label key shape, values of at most
// CustomMetricLabelValueMaxBytes bytes. It returns "" when valid.
func ValidateCustomMetricLabels(labels map[string]string) string {
	if len(labels) > CustomMetricMaxLabels {
		return fmt.Sprintf("at most %d labels", CustomMetricMaxLabels)
	}
	for k, v := range labels {
		if !ValidCustomMetricLabelKey(k) {
			return fmt.Sprintf("label %q must match [a-z_][a-z0-9_]{0,62}", k)
		}
		if len(v) > CustomMetricLabelValueMaxBytes {
			return fmt.Sprintf("label %q is longer than %d bytes", k, CustomMetricLabelValueMaxBytes)
		}
	}
	return ""
}
