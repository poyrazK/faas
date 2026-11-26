package main

import (
	"fmt"
	"slices"
	"strings"
)

// jobReplayCommandNote says when a replay runs a command other than the
// Job's current one. A run captures its command, and replay re-runs the
// source run's capture (prod hunt r56: after `job update --command` fixed the
// bug, `job replay` ran the old command and failed again). Empty when the
// commands match or the run predates command capture.
func jobReplayCommandNote(job string, current, replayed []string) string {
	if len(replayed) == 0 || slices.Equal(current, replayed) {
		return ""
	}
	return fmt.Sprintf("Replay runs the source run's command [%s], not the Job's current command [%s]; to run the current command start a new run: gregale job run %s",
		oneLine(strings.Join(replayed, " ")), oneLine(strings.Join(current, " ")), job)
}
