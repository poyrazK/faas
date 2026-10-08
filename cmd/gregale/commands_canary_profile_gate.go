package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"strings"
)

func cmdCanaryProfileGate(args []string) int {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return printErr("Usage", fmt.Errorf("gregale canary gate DEPLOYMENT_ID [--json]"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.GetProfileCanaryGate(context.Background(), args[0])
	if err != nil {
		return printErr("Could not read profiling gate", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	fmt.Fprintf(osStdout, "Profiling gate: %s · policy %d · stage %d\n%s\n", out.Status, out.PolicyRevision, out.CanaryStep, out.Reason)
	if out.Deadline != nil {
		fmt.Fprintf(osStdout, "Deadline: %s · timeout action: %s\n", out.Deadline.UTC().Format("2006-01-02T15:04:05Z"), out.OnTimeout)
	}
	if out.Signal != nil && out.Signal.Gate != nil {
		for _, r := range out.Signal.Gate.Streaks {
			fmt.Fprintf(osStdout, "%s: %s (%d consecutive windows)\n", r.Route, r.Status, r.Count)
		}
	}
	return 0
}

func cmdCanaryAdvance(args []string) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return printErr("Usage", fmt.Errorf("gregale canary advance DEPLOYMENT_ID --expected-step N [--profile-policy-revision REV --profile-override-reason TEXT]"))
	}
	fs := newFlagSet("canary advance", flag.ContinueOnError)
	step := fs.Int("expected-step", -1, "observed stage to advance")
	revision := fs.Int64("profile-policy-revision", -1, "current profile policy revision for an explicit override")
	reason := fs.String("profile-override-reason", "", "audited reason for overriding only the profiling gate")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *step < 0 || (*revision >= 0) != (strings.TrimSpace(*reason) != "") || len(*reason) > api.ProfileGateOverrideMaxReasonBytes {
		return printErr("Invalid advance", fmt.Errorf("require --expected-step; an override needs both the current revision and a bounded nonblank reason"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var out api.CanaryAdvanceResponse
	if *revision >= 0 {
		out, err = client.AdvanceCanaryWithProfileOverride(context.Background(), args[0], *step, api.ProfileGateOverride{ExpectedPolicyRevision: *revision, Reason: *reason})
	} else {
		out, err = client.AdvanceCanary(context.Background(), args[0], *step)
	}
	if err != nil {
		return printErr("Could not advance canary", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	fmt.Fprintf(osStdout, "Canary %s: traffic %d%% · audit %s\n", args[0], out.Deployment.TrafficPercent, out.AuditID)
	return 0
}
