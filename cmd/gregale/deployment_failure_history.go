package main

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

func renderFailedDeploymentHistory(page api.DeploymentListResponse, app string, limit int, wide bool) int {
	if len(page.Items) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No failed deployments found in fetched history. Deployment states may change; this does not establish application health.")
	}
	for _, deployment := range page.Items {
		if wide {
			renderDeploymentRowWide(osStdout, deployment)
		} else {
			renderDeploymentRow(osStdout, deployment)
		}
		if deploymentIDPattern.MatchString(deployment.ID) {
			_, _ = fmt.Fprintf(osStdout, "  Inspect failure (POSIX shells): %s deployment %s\n", recoveryCLI(), recoveryArgument(deployment.ID))
			if app != "" && api.ValidAppSlug(app) {
				printDeploymentLogCommand(osStdout, app, deployment.ID)
			}
		}
	}
	if len(page.Items) > 0 && app == "" {
		_, _ = fmt.Fprintln(osStdout, "For scoped log commands, repeat this history read with --app APP_SLUG.")
	}
	if page.NextBefore != "" {
		appArg, wideArg := "", ""
		if app != "" {
			appArg = " --app " + recoveryArgument(app)
		}
		if wide {
			wideArg = " --wide"
		}
		_, _ = fmt.Fprintf(osStdout, "Next history page with this filter (POSIX shells):\n  %s deployments%s --failed-only --limit %d --cursor %s%s\n", recoveryCLI(), appArg, limit, recoveryArgument(page.NextBefore), wideArg)
	}
	return 0
}
