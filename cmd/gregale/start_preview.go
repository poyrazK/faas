package main

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

func (r *startRunner) renderLaunchDefaults(report doctorReport, app api.AppResponse, exists bool) {
	_, _ = fmt.Fprintf(osStdout, "  Connection  %s\n", currentProfile())
	if report.Profile != nil && report.Profile.Framework != "unknown" {
		_, _ = fmt.Fprintf(osStdout, "  Build       %s (auto-detected)\n", report.Profile.Framework)
	} else if detectShape(r.session.SourcePath) == shapeFunction {
		_, _ = fmt.Fprintln(osStdout, "  Build       Function runtime detected from source")
	}
	if !exists {
		// App creation deliberately leaves resource values to the API's plan
		// defaults, matching the normal deploy path without duplicating policy.
		_, _ = fmt.Fprintln(osStdout, "  Resources   Account plan defaults")
	} else if app.RAMMB > 0 && app.CPUMillicores > 0 {
		_, _ = fmt.Fprintf(osStdout, "  Resources   %d MB RAM / %d millicores (existing app)\n", app.RAMMB, app.CPUMillicores)
	} else {
		_, _ = fmt.Fprintln(osStdout, "  Resources   Keep existing app settings")
	}
}
