package s3gateway

import (
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/prometheus/client_golang/prometheus"
)

func registerRetentionMetrics(h *DiskHandler, registry *prometheus.Registry) {
	for name, gauge := range map[string]struct {
		help string
		read func(objectnode.RetentionStatus) float64
	}{
		"base_sequence":        {"First retained journal prefix; older followers require reseeding.", func(s objectnode.RetentionStatus) float64 { return float64(s.BaseSequence) }},
		"applied_sequence":     {"Latest durable applied sequence; monotonic across compaction.", func(s objectnode.RetentionStatus) float64 { return float64(s.AppliedSequence) }},
		"safe_sequence":        {"Largest prefix eligible for compaction under current follower pins.", func(s objectnode.RetentionStatus) float64 { return float64(s.SafeSequence) }},
		"pinned_records":       {"Applied records held by registered followers or replica durability.", func(s objectnode.RetentionStatus) float64 { return float64(s.AppliedSequence - s.SafeSequence) }},
		"registered_followers": {"Explicit follower retention registrations.", func(s objectnode.RetentionStatus) float64 { return float64(s.Followers) }},
		"records":              {"Retained physical journal files, including an uncertain last publication.", func(s objectnode.RetentionStatus) float64 { return float64(s.Usage.Records) }},
		"blobs":                {"Retained replay payload files, including unpublished payloads.", func(s objectnode.RetentionStatus) float64 { return float64(s.Usage.Blobs) }},
		"journal_bytes":        {"Retained journal file bytes; cached without scrape-time disk reads.", func(s objectnode.RetentionStatus) float64 { return float64(s.Usage.JournalBytes) }},
		"blob_bytes":           {"Retained replay payload bytes, including unpublished payloads.", func(s objectnode.RetentionStatus) float64 { return float64(s.Usage.BlobBytes) }},
	} {
		registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "faas_object_node_retention_" + name, Help: gauge.help}, func() float64 {
			status, err := h.config.Store.RetentionStatus()
			if err != nil {
				return 0
			}
			return gauge.read(status)
		}))
	}
}
