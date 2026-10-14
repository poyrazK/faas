package main

import "fmt"

type jobDefaultsRecoveryReadiness struct {
	recordedRecoveryReadiness
	PreviewCommand string `json:"preview_command,omitempty"`
}

func inspectJobDefaultsRecovery(defaults localJobDefaults, path string) jobDefaultsRecoveryReadiness {
	readiness := inspectRecordedRecoveryReadiness(path, []string{"jobs", "resume", "--saved-job"}, "No local records match the saved Job on this connection; inspect recorded runs and the saved selector. Remote history was not checked.", func(record acceptedRunRecord) bool {
		return record.Kind == "job" && record.Name == defaults.Job
	})
	report := jobDefaultsRecoveryReadiness{recordedRecoveryReadiness: readiness}
	if readiness.LatestReady {
		report.PreviewCommand = savedNameSuggestionCommand([]string{"jobs", "resume", "--saved-job", "--latest", "--dry-run"})
	}
	return report
}

func renderJobDefaultsRecovery(recovery jobDefaultsRecoveryReadiness) {
	renderRecordedRecoveryReadiness(recovery.recordedRecoveryReadiness, "Local Job recovery readiness")
	if recovery.PreviewCommand != "" {
		_, _ = fmt.Fprintf(osStdout, "Preview recovery (offline; POSIX shells):\n  %s\n", recovery.PreviewCommand)
	}
}
