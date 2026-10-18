package main

import (
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/jobresult"
)

func renderJobTasksWithArtifacts(name, runID string, tasks []api.JobTaskResponse, manifests map[int]jobresult.Manifest, offset int, statusFilter string, includes, excludes []string) int {
	_, _ = fmt.Fprintf(osStdout, "Job: %s; run: %s; task artifacts starting at offset %d\n", name, runID, offset)
	if len(includes) > 0 || len(excludes) > 0 {
		_, _ = fmt.Fprintln(osStdout, "Artifact counts and listing/download commands use the same "+jobArtifactFilterDescription(excludes)+".")
	}
	if len(tasks) == 0 {
		qualifier := ""
		if statusFilter != "" {
			qualifier = " matching " + statusFilter
		}
		description := "recorded artifacts"
		if len(includes) > 0 || len(excludes) > 0 {
			description += " matching the " + jobArtifactFilterDescription(excludes)
		}
		_, _ = fmt.Fprintf(osStdout, "No tasks%s with %s found from this offset. Active tasks may produce outputs later.\n", qualifier, description)
		return 0
	}
	for _, task := range tasks {
		manifest := manifests[task.TaskIndex]
		managed := 0
		for _, artifact := range manifest.Artifacts {
			if strings.HasPrefix(artifact.URI, "obj://") {
				managed++
			}
		}
		_, _ = fmt.Fprintf(osStdout, "\nTask %d; attempt %d; status %s; artifacts: %d managed, %d external\n", task.TaskIndex, task.Attempt, oneLine(task.Status), managed, len(manifest.Artifacts)-managed)
		listingCommand := fmt.Sprintf("%s jobs artifacts %s %s %d", recoveryCLI(), recoveryArgument(name), recoveryArgument(runID), task.TaskIndex)
		listingCommand = appendJobArtifactFilters(listingCommand, includes, excludes)
		_, _ = fmt.Fprintf(osStdout, "  List recorded artifacts (POSIX shells):\n    %s\n", listingCommand)
		if managed > 0 {
			directory := fmt.Sprintf("./task-%d-artifacts", task.TaskIndex)
			command := fmt.Sprintf("%s jobs artifact-download %s %s %d --all --out-dir %s", recoveryCLI(), recoveryArgument(name), recoveryArgument(runID), task.TaskIndex, recoveryArgument(directory))
			command = appendJobArtifactFilters(command, includes, excludes)
			_, _ = fmt.Fprintf(osStdout, "  Download managed artifacts to a directory containing no conflicting files (created when missing) (POSIX shells):\n    %s\n", command)
		}
		if managed < len(manifest.Artifacts) {
			_, _ = fmt.Fprintln(osStdout, "  External artifacts require access through their storage provider.")
		}
	}
	_, _ = fmt.Fprintln(osStdout, "\nRecorded current-attempt metadata; stored bytes are verified only when downloaded. Outputs can change after a task retry.")
	return 0
}
