package faas

func boundedIssueContext(in IssueContext) ([]IssueBreadcrumb, int, map[string]string) {
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
	out := append([]IssueBreadcrumb(nil), entries...)
	for i := range out {
		out[i].Message = boundedIssueString(out[i].Message, 256)
	}
	tags := map[string]string{}
	for _, key := range []string{"component", "operation", "feature_flag", "feature_variant", "dependency", "region", "job_type", "workflow", "state", "error_class", "release_channel", "runtime"} {
		if value, ok := in.Tags[key]; ok {
			tags[key] = boundedIssueString(value, 128)
		}
	}
	return out, dropped, tags
}
