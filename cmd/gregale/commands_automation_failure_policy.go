package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdAutomationsFailurePolicy(args []string) int {
	fs := newFlagSet("automations failure-policy", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	name := fs.String("name", "", "automation name")
	enabled := fs.Bool("enabled", false, "enable or disable failure monitoring; does not clear an existing pause")
	version := fs.Int64("expected-version", -1, "current failure policy version; required to configure")
	threshold := fs.Int("failure-threshold", api.AutomationFailurePolicyDefaultThreshold, "terminal failures required to pause")
	minRuns := fs.Int("min-completed-runs", api.AutomationFailurePolicyDefaultMinRuns, "minimum completed runs in the observation window")
	window := fs.Int("window-seconds", api.AutomationFailurePolicyDefaultWindowSeconds, "terminal outcome observation window in seconds")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	setting, enabledSet := false, false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "enabled":
			setting = true
			enabledSet = true
		case "expected-version", "failure-threshold", "min-completed-runs", "window-seconds":
			setting = true
		}
	})
	if strings.TrimSpace(*app) == "" || strings.TrimSpace(*name) == "" || setting && (!enabledSet || *version < 0 || *threshold < 1 || *threshold > api.AutomationFailurePolicyMaxCount || *minRuns < 1 || *minRuns > api.AutomationFailurePolicyMaxCount || *window < api.AutomationFailurePolicyMinWindowSeconds || *window > api.AutomationFailurePolicyMaxWindowSeconds) {
		return printErr("Invalid failure policy options", errors.New("use --app and --name; configuration also requires --enabled and --expected-version with valid thresholds"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var out api.AutomationFailurePolicyResponse
	if setting {
		out, err = client.SetAutomationFailurePolicy(context.Background(), *app, *name, api.SetAutomationFailurePolicyRequest{Enabled: enabled, ExpectedVersion: *version, FailureThreshold: *threshold, MinCompletedRuns: *minRuns, WindowSeconds: *window})
	} else {
		out, err = client.GetAutomationFailurePolicy(context.Background(), *app, *name)
	}
	if err != nil {
		return printErr("Could not read or configure failure policy", err)
	}
	return printAutomationFailurePolicy(out)
}
func cmdAutomationsFailureResume(args []string) int {
	fs := newFlagSet("automations failure-resume", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	name := fs.String("name", "", "automation name")
	generation := fs.Int64("expected-generation", 0, "current failure pause generation")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*app) == "" || strings.TrimSpace(*name) == "" || *generation <= 0 {
		return printErr("Invalid resume options", errors.New("use --app, --name and a positive --expected-generation"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	preview, err := client.GetAutomationFailurePolicy(context.Background(), *app, *name)
	if err != nil {
		return printErr("Could not preview failure pause resume", err)
	}
	if !preview.Paused || preview.Generation != *generation {
		return printErr("Failure pause changed", errors.New("reload failure-policy and use its current generation"))
	}
	if !jsonOutput {
		if _, err = fmt.Fprintf(osStdout, "Resume preview: %d pending runs, %d running runs, %d waiting runs, %d retained events. Existing runs continue; retained events remain subject to routing retention.\n", preview.PendingRuns, preview.RunningRuns, preview.WaitingRuns, preview.RetainedEvents); err != nil {
			return printErr("Could not write resume preview", err)
		}
	}
	out, err := client.ResumeAutomationFailurePause(context.Background(), *app, *name, api.ResumeAutomationFailurePauseRequest{ExpectedGeneration: *generation})
	if err != nil {
		return printErr("Could not resume failure pause", err)
	}
	return printAutomationFailurePolicy(out)
}
func printAutomationFailurePolicy(out api.AutomationFailurePolicyResponse) int {
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, err := fmt.Fprintf(osStdout, "Failure policy: enabled=%t, version=%d; pause after %d failures and at least %d completed runs in %ds.\nFailure pause: paused=%t, generation=%d; observed failures=%d, completed=%d.\nResume preview: pending=%d, running=%d, waiting=%d, retained events=%d.\n", out.Policy.Enabled, out.Policy.Version, out.Policy.FailureThreshold, out.Policy.MinCompletedRuns, out.Policy.WindowSeconds, out.Paused, out.Generation, out.ObservedFailures, out.ObservedCompletedRuns, out.PendingRuns, out.RunningRuns, out.WaitingRuns, out.RetainedEvents)
	if err != nil {
		return printErr("Could not write failure policy", err)
	}
	return 0
}
