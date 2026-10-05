package main

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

func renderRouteClientErrors(c *api.RouteHealthClientErrorReport, indent string) {
	if c == nil {
		return
	}
	_, _ = fmt.Fprintf(osStdout, "%sWatched client responses: %s; advisory (%s)\n", indent, c.Status, previewReportText(c.Reason))
	for _, f := range c.Statuses {
		_, _ = fmt.Fprintf(osStdout, "%s  HTTP %d: %s (%s)\n", indent, f.StatusCode, f.Status, previewReportText(f.Reason))
		for _, w := range f.Windows {
			_, _ = fmt.Fprintf(osStdout, "%s    %s–%s candidate %d/%d (%.1f%%), stable %d/%d (%.1f%%): %s (%s)\n", indent, w.Start.Format("15:04:05Z"), w.End.Format("15:04:05Z"), w.Candidate.Responses, w.Candidate.Requests, w.Candidate.Rate*100, w.Stable.Responses, w.Stable.Requests, w.Stable.Rate*100, w.Status, previewReportText(w.Reason))
		}
	}
}
