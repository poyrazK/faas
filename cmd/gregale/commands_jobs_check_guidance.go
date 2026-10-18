package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func jobReadinessRecoveryCommands(name, state, image string, run *jobOverviewRun, checkRun bool, actions []jobOverviewAction) []jobOverviewAction {
	commands := []jobOverviewAction{}
	seen := map[string]bool{}
	add := func(purpose, command string) {
		if !seen[command] {
			seen[command] = true
			commands = append(commands, jobOverviewAction{purpose, command})
		}
	}
	command := func(args ...string) string {
		return savedNameSuggestionCommand(append([]string{"jobs"}, args...))
	}
	if state != "active" {
		add("Inspect Job state", command("info", name))
	}
	switch image {
	case "ready":
	case "pending", "verifying_legacy":
		add("Wait for image preparation", command("info", name, "--wait-ready"))
	default:
		add("Inspect image preparation", command("info", name))
	}
	if checkRun {
		if run == nil {
			add("Preview a first run", command("run", name, "--dry-run"))
		} else {
			switch run.Status {
			case "succeeded":
			case "queued", "running":
				add("Wait for this run", command("wait", name, run.ID))
				add("Preview cancellation", command("cancel", name, run.ID, "--dry-run"))
			case "failed", "dead_letter", "cancelled":
				add("Inspect unsuccessful tasks", command("tasks", name, run.ID, "--unsuccessful-only"))
				add("Read unsuccessful-task logs", command("logs", name, run.ID, "--unsuccessful-only"))
				add("Preview replay of unsuccessful tasks", command("replay-failed", name, run.ID, "--dry-run"))
				for _, action := range actions {
					if strings.HasPrefix(action.Purpose, "Read task ") {
						add(action.Purpose, action.Command)
					}
				}
			default:
				add("Inspect task outcomes", command("tasks", name, run.ID, "--all"))
			}
		}
	}
	return commands
}

func printJobReadinessCheckFailure(title, detail string, commands []jobOverviewAction) int {
	if jsonOutput {
		problem := diagnosticProblem(api.Problem{Status: 400, Code: "invalid_request", Title: title, Detail: redactErrorText(detail)})
		receipt := struct {
			api.Problem
			Category          string              `json:"category"`
			ExitCode          int                 `json:"exit_code"`
			SuggestedCommands []jobOverviewAction `json:"suggested_commands"`
		}{problem, exitCategory(1), 1, commands}
		data, err := json.Marshal(receipt)
		if err != nil {
			return printErr("Could not encode readiness guidance", err)
		}
		_, _ = fmt.Fprintln(osStderr, string(data))
		return 1
	}
	printErr(title, fmt.Errorf("%s", detail))
	_, _ = fmt.Fprintln(osStderr, "Suggested next commands (POSIX shells):")
	for _, command := range commands {
		_, _ = fmt.Fprintf(osStderr, "%s:\n  %s\n", command.Purpose, command.Command)
	}
	return 1
}
