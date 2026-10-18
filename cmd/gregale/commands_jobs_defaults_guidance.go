package main

import "fmt"

func renderMissingJobDefaults(path string) int {
	command := savedNameSuggestionCommand([]string{"jobs", "defaults", "set", "my-job", "--tasks", "1"})
	note := "Replace my-job with your Job name and 1 with the task count you need."
	if jsonOutput {
		return jsonOut(writeJSONSingle(struct {
			Saved                bool   `json:"saved"`
			Path                 string `json:"path"`
			SetupCommandTemplate string `json:"setup_command_template"`
			Note                 string `json:"note"`
		}{false, path, command, note}))
	}
	_, _ = fmt.Fprintf(osStdout, "No Job defaults saved for this checkout.\nPath: %s\nSetup example (POSIX shells):\n  %s\n%s\n", oneLine(path), command, note)
	return 0
}

type jobDefaultsNextStep struct {
	PreviewCommand         string `json:"preview_command,omitempty"`
	PreviewCommandTemplate string `json:"preview_command_template,omitempty"`
	NeedsTaskCountOrInputs bool   `json:"needs_task_count_or_inputs"`
	Note                   string `json:"note"`
}

func jobDefaultsRunGuidance(d localJobDefaults) jobDefaultsNextStep {
	command := "gregale --profile " + recoveryArgument(currentProfile()) + " jobs run " + recoveryArgument(d.Job)
	if d.Tasks == 0 {
		return jobDefaultsNextStep{
			PreviewCommandTemplate: command + " --tasks N --dry-run",
			NeedsTaskCountOrInputs: true,
			Note:                   "Task count is unset. Replace N with a positive count, or supply explicit inputs instead of --tasks. The preview uses the saved defaults in this checkout.",
		}
	}
	return jobDefaultsNextStep{
		PreviewCommand: command + " --dry-run",
		Note:           "Uses saved defaults in this checkout. Running this command may read API metadata; it does not submit a Job.",
	}
}

func writeJobDefaultsReceipt(d localJobDefaults, path string) int {
	return writeJobDefaultsReceiptWithRecovery(d, path, nil)
}

func writeJobDefaultsReceiptWithRecovery(d localJobDefaults, path string, recovery *jobDefaultsRecoveryReadiness) int {
	return jsonOut(writeJSONSingle(struct {
		Defaults localJobDefaults              `json:"defaults"`
		Path     string                        `json:"path"`
		NextStep jobDefaultsNextStep           `json:"next_step"`
		Recovery *jobDefaultsRecoveryReadiness `json:"recovery,omitempty"`
	}{d, path, jobDefaultsRunGuidance(d), recovery}))
}

func renderJobDefaultsGuidance(d localJobDefaults) {
	step := jobDefaultsRunGuidance(d)
	if step.NeedsTaskCountOrInputs {
		_, _ = fmt.Fprintf(osStdout, "%s\nRun-preview template (POSIX shells; replace N):\n  %s\n", step.Note, step.PreviewCommandTemplate)
	} else {
		_, _ = fmt.Fprintf(osStdout, "Preview a run (POSIX shells):\n  %s\n%s\n", step.PreviewCommand, step.Note)
	}
}
