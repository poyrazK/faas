package main

import (
	"errors"
	"fmt"
	"os"
)

type logViewPreferenceGuidance struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Command string `json:"command"`
}

func inspectRemovedLogViewPreference(name, replacement string) *logViewPreferenceGuidance {
	defaults, _, err := readLogDefaults()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return &logViewPreferenceGuidance{
			Status:  "unknown",
			Message: "Local preferences could not be safely read. Inspect the preferred view after this operation.",
			Command: savedNameSuggestionCommand([]string{"logs", "defaults"}),
		}
	}
	if defaults.View != name {
		return nil
	}
	args := []string{"logs", "defaults", "clear"}
	message := fmt.Sprintf("The preferred-view setting still points to %s. Clear it or select another saved view with logs defaults set NAME.", name)
	if replacement != "" {
		args = []string{"logs", "defaults", "set", replacement}
		message = fmt.Sprintf("The preferred-view setting still points to %s. Select the renamed view with the command below.", name)
	}
	return &logViewPreferenceGuidance{Status: "requires_update", Message: message, Command: savedNameSuggestionCommand(args)}
}

func renderLogViewPreferenceGuidance(guidance *logViewPreferenceGuidance) {
	if guidance != nil {
		_, _ = fmt.Fprintf(osStdout, "%s\nPreference command (POSIX shells):\n  %s\n", guidance.Message, guidance.Command)
	}
}
