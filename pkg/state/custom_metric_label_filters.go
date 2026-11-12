package state

import (
	"encoding/json"
	"maps"
)

// customMetricLabelsJSON encodes a label filter (ADR-20261108160000 slice
// C2) for a nullable jsonb column; no labels is NULL, the aggregate.
func customMetricLabelsJSON(labels map[string]string) []byte {
	if len(labels) == 0 {
		return nil
	}
	b, err := json.Marshal(labels)
	if err != nil {
		return nil
	}
	return b
}

// customMetricLabelsFromJSON decodes a label filter column. NULL, or a
// value that is not an object of strings, reads as no filter.
func customMetricLabelsFromJSON(b []byte) map[string]string {
	if len(b) == 0 {
		return nil
	}
	var labels map[string]string
	if err := json.Unmarshal(b, &labels); err != nil || len(labels) == 0 {
		return nil
	}
	return labels
}

// cloneCustomMetricLabels copies a filter so MemStore never shares a map
// with its caller.
func cloneCustomMetricLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	return maps.Clone(labels)
}
