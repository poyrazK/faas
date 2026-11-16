package preview

import (
	"strings"
	"time"
	"unicode/utf8"

	faas "github.com/gregale-dev/gregale-go"
)

func boundedIssueContext(in IssueContext, occurredAt time.Time) ([]faas.IssueBreadcrumb, int, map[string]string) {
	dropped := in.BreadcrumbsDropped
	if dropped < 0 {
		dropped = 0
	}
	if dropped > 2147483647 {
		dropped = 2147483647
	}
	entries := in.Breadcrumbs
	if len(entries) > 16 {
		extra := len(entries) - 16
		if extra > 2147483647-dropped {
			dropped = 2147483647
		} else {
			dropped += extra
		}
		entries = entries[extra:]
	}
	out := make([]faas.IssueBreadcrumb, 0, len(entries))
	for _, entry := range entries {
		validCategory := entry.Category == "http" || entry.Category == "db" || entry.Category == "cache" || entry.Category == "workflow" || entry.Category == "log" || entry.Category == "custom"
		validLevel := entry.Level == "debug" || entry.Level == "info" || entry.Level == "warning" || entry.Level == "error"
		if !validCategory || !validLevel || entry.OccurredAt.IsZero() || entry.OccurredAt.Year() < 1 || entry.OccurredAt.Year() > 9999 || entry.OccurredAt.After(occurredAt.Add(5*time.Minute)) || !utf8.ValidString(entry.Message) || strings.ContainsRune(entry.Message, 0) {
			if dropped < 2147483647 {
				dropped++
			}
			continue
		}
		entry.Message = boundedIssueString(entry.Message, 256)
		out = append(out, entry)
	}
	tags := map[string]string{}
	for _, key := range []string{"component", "operation", "feature_flag", "feature_variant", "dependency", "region", "job_type", "workflow", "state", "error_class", "release_channel", "runtime"} {
		if value, ok := in.Tags[key]; ok && utf8.ValidString(value) && !strings.ContainsRune(value, 0) {
			tags[key] = boundedIssueString(value, 128)
		}
	}
	return out, dropped, tags
}
