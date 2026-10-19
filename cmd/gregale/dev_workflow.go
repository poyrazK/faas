package main

import (
	"fmt"
	"strings"
	"time"
)

func checkDevSource(source string, config devSourceConfig) int {
	summary := buildDevSetupDoctorSummary(source, config.shape)
	if summary.Errors > 0 {
		var reasons []string
		for _, check := range summary.Checks {
			if check.Status == "error" {
				reasons = append(reasons, check.Name+": "+check.Hint)
			}
		}
		return printErr("Developer source checks failed", fmt.Errorf("%s; next: %s doctor --path %s", strings.Join(reasons, "; "), recoveryCLI(), recoveryArgument(source)))
	}
	if !jsonOutput {
		PrintOK(osStdout, "Developer source checks passed (%d warnings)", summary.Warnings)
		for _, check := range summary.Checks {
			if check.Status == "warn" && check.Hint != "" {
				PrintWarn(osStderr, "%s", check.Hint)
			}
		}
	}
	return 0
}

func renderDevSessionExit(source, project, app string, lease time.Duration) {
	if jsonOutput {
		return
	}
	args := " --path " + recoveryArgument(source) + " --name " + recoveryArgument(project)
	_, _ = fmt.Fprintf(osStdout, "\nDeveloper session %s remains available for its lease (%s after the latest sync).\n  Inspect: %s dev info%s\n  Stop:    %s dev%s --stop\n", app, formatDevLease(lease), recoveryCLI(), args, recoveryCLI(), args)
}
