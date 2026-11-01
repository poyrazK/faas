package reposcan

import (
	"fmt"
	"maps"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/labels"
)

// k8sLabels returns the metadata.labels of a Deployment or CronJob that its
// app can carry (ADR-20261031100025 §7): it drops gregale.dev/ keys, which
// belong to Gregale, and keys or values the label rules refuse, and keeps at
// most api.LabelsPerResourceMax in key order. Each label it drops is a scan
// warning. Pod template labels stay matching-only.
func k8sLabels(warnings *[]string, src, kind string, m k8sManifest) map[string]string {
	if len(m.Metadata.Labels) == 0 {
		return nil
	}
	drop := func(format string, args ...any) {
		*warnings = append(*warnings, fmt.Sprintf("reposcan: %s %s %q: ", src, kind, m.Metadata.Name)+fmt.Sprintf(format, args...))
	}
	out := make(map[string]string, len(m.Metadata.Labels))
	for _, key := range slices.Sorted(maps.Keys(m.Metadata.Labels)) {
		value := m.Metadata.Labels[key]
		switch {
		case labels.Reserved(key):
			drop("label %q not imported: keys under %s/ belong to Gregale", key, labels.ReservedPrefix)
		case labels.ValidateKey(key) != nil:
			drop("label not imported: %v", labels.ValidateKey(key))
		case labels.ValidateValue(key, value) != nil:
			drop("label not imported: %v", labels.ValidateValue(key, value))
		case len(out) == api.LabelsPerResourceMax:
			drop("label %q not imported: an app carries at most %d labels", key, api.LabelsPerResourceMax)
		default:
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
