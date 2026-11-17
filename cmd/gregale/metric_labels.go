package main

import (
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// metricLabelFlags turns repeated --metric-label key=value flags into a
// custom metric label filter (ADR-20261108160000 slice C2): the one labelled
// series with exactly these labels. No flags is nil, the metric's aggregate.
func metricLabelFlags(values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	labels := make(map[string]string, len(values))
	for _, v := range values {
		key, value, ok := strings.Cut(v, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("--metric-label must be key=value; got %q", v)
		}
		if _, dup := labels[key]; dup {
			return nil, fmt.Errorf("--metric-label %s given twice", key)
		}
		labels[key] = value
	}
	if msg := api.ValidateCustomMetricLabels(labels); msg != "" {
		return nil, fmt.Errorf("--metric-label: %s", msg)
	}
	return labels, nil
}

// metricLabelSuffix renders a label filter as {k=v,...} for output; "" for none.
func metricLabelSuffix(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	return "{" + api.CustomMetricSeriesKey(labels) + "}"
}
