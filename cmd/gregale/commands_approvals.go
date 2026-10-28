package main

// ADR-20261020085706: release approvals.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdApprovals(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale release approval <require|policy|off|pending|status|approve|reject>", "approvals")
		return 1
	}
	switch args[0] {
	case "require":
		return approvalsRequire(args[1:])
	case "policy":
		return approvalsPolicy(args[1:])
	case "off":
		return approvalsOff(args[1:])
	case "pending":
		return approvalsPending(args[1:])
	case "status":
		return approvalsStatus(args[1:])
	case "approve":
		return approvalsDecide(args[1:], api.ReleaseApprovalDecisionApprove)
	case "reject":
		return approvalsDecide(args[1:], api.ReleaseApprovalDecisionReject)
	}
	printCommandValidation(os.Stderr, "gregale release approval: unknown subcommand %q\n", args[0])
	return unknownSubcommandChoices(os.Stderr, "approvals", nestedCliCommand("approvals"), args[0])
}

// approvalsApp resolves --app from the flag or the linked project.
func approvalsApp(app, usage string) (string, bool) {
	resolved, err := resolveAppFlagOrContext(app)
	if err != nil && !errors.Is(err, errProjectContextNotFound) {
		printErr("Could not read local project context", err)
		return "", false
	}
	if resolved == "" {
		PrintUsage(os.Stderr, usage, "approvals")
		return "", false
	}
	return resolved, true
}

// approvalPolicyRequest builds a policy request from the CLI flags.
func approvalPolicyRequest(approvals, afterStage int, timeout, approvers string, allowSelf bool, notify string) (api.SetReleaseApprovalPolicyRequest, error) {
	req := api.SetReleaseApprovalPolicyRequest{RequiredApprovals: approvals, AfterStage: afterStage, Approvers: approvers, AllowSelfApproval: allowSelf}
	for _, name := range strings.Split(notify, ",") {
		if name = strings.TrimSpace(name); name != "" {
			req.NotifyChannels = append(req.NotifyChannels, name)
		}
	}
	if timeout != "" {
		d, err := time.ParseDuration(timeout)
		if err != nil || d <= 0 || d%time.Minute != 0 {
			return req, fmt.Errorf("--timeout must be a whole number of minutes such as 30m or 24h")
		}
		req.TimeoutMinutes = int(d / time.Minute)
	}
	return req.Normalize()
}

func approvalsRequire(args []string) int {
	fs := newFlagSet("approvals require", flag.ContinueOnError)
	app := fs.String("app", "", "app slug (defaults to linked project app)")
	approvals := fs.Int("approvals", 0, "approvals each production release needs (1-5)")
	afterStage := fs.Int("after-stage", 0, "canary stage the release waits in (default 1)")
	timeout := fs.String("timeout", "", "abort a release that waits longer (default 24h)")
	approvers := fs.String("approvers", "", "who may approve: admin (owners and admins, default) or developer")
	allowSelf := fs.Bool("allow-self-approval", false, "count the deployer's own approval")
	notify := fs.String("notify", "", "comma-separated Slack or email channels told when a release waits, is decided or times out")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	slug, ok := approvalsApp(*app, "usage: gregale release approval require [--app <slug>] --approvals N [--after-stage N] [--timeout 24h] [--approvers admin|developer] [--allow-self-approval] [--notify CHANNEL,...]")
	if !ok {
		return 1
	}
	req, err := approvalPolicyRequest(*approvals, *afterStage, *timeout, *approvers, *allowSelf, *notify)
	if err != nil {
		return printErr("Invalid approval policy", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	policy, err := client.SetReleaseApprovalPolicy(context.Background(), slug, req)
	if err != nil {
		return printErr("Could not set the approval policy", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(policy))
	}
	_, _ = fmt.Fprintf(osStdout, "%s now requires approval for production releases: %s.\n", slug, approvalPolicySummary(policy))
	return 0
}

func approvalPolicySummary(p api.ReleaseApprovalPolicy) string {
	who := "owners or admins"
	if p.Approvers == api.ReleaseApproversDeveloper {
		who = "owners, admins or developers"
	}
	self := "not the deployer"
	if p.AllowSelfApproval {
		self = "the deployer counts"
	}
	notify := "no notifications"
	if len(p.NotifyChannels) > 0 {
		notify = "notifying " + strings.Join(p.NotifyChannels, ", ")
	}
	return fmt.Sprintf("%d approval(s) from %s (%s), waiting in canary stage %d, aborted after %s, %s",
		p.RequiredApprovals, who, self, p.AfterStage, (time.Duration(p.TimeoutMinutes) * time.Minute).String(), notify)
}

func approvalsPolicy(args []string) int {
	fs := newFlagSet("approvals policy", flag.ContinueOnError)
	app := fs.String("app", "", "app slug (defaults to linked project app)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	slug, ok := approvalsApp(*app, "usage: gregale release approval policy [--app <slug>]")
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	policy, err := client.GetReleaseApprovalPolicy(context.Background(), slug)
	if err != nil {
		return printErr("Could not read the approval policy", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(policy))
	}
	_, _ = fmt.Fprintf(osStdout, "%s: %s.\nLast changed by %s at %s.\n", slug, approvalPolicySummary(policy), policy.UpdatedBy, policy.UpdatedAt)
	return 0
}

func approvalsOff(args []string) int {
	fs := newFlagSet("approvals off", flag.ContinueOnError)
	app := fs.String("app", "", "app slug (defaults to linked project app)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	slug, ok := approvalsApp(*app, "usage: gregale release approval off [--app <slug>]")
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteReleaseApprovalPolicy(context.Background(), slug); err != nil {
		return printErr("Could not remove the approval policy", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"app": slug, "removed": true}))
	}
	_, _ = fmt.Fprintf(osStdout, "%s no longer requires release approvals.\n", slug)
	return 0
}

func approvalsPending(args []string) int {
	fs := newFlagSet("approvals pending", flag.ContinueOnError)
	app := fs.String("app", "", "app slug (defaults to linked project app)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	slug, ok := approvalsApp(*app, "usage: gregale release approval pending [--app <slug>]")
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.ListReleaseApprovals(context.Background(), slug)
	if err != nil {
		return printErr("Could not list releases waiting for approval", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	if len(out.Items) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No releases of %s are waiting for approval.\n", slug)
		return 0
	}
	w := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "DEPLOYMENT\tDEPLOYED BY\tAPPROVALS\tSTATE\tEXPIRES")
	for _, s := range out.Items {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%d/%d\t%s\t%s\n", s.DeploymentID, orDash(s.DeployedBy), s.Approvals, s.RequiredApprovals, s.State, orDash(s.ExpiresAt))
	}
	_ = w.Flush()
	return 0
}

func approvalsStatus(args []string) int {
	fs := newFlagSet("approvals status", flag.ContinueOnError)
	app := fs.String("app", "", "app slug, to resolve a vN revision (defaults to linked project app)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale release approval status <deployment> [--app <slug>]", "approvals")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	id, err := resolveDeploymentArg(context.Background(), client, *app, fs.Arg(0))
	if err != nil {
		return printErr("Could not resolve the deployment", err)
	}
	status, err := client.GetReleaseApproval(context.Background(), id)
	if err != nil {
		return printErr("Could not read the release's approvals", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(status))
	}
	printApprovalStatus(status)
	return 0
}

func approvalsDecide(args []string, decision string) int {
	fs := newFlagSet("approvals "+decision, flag.ContinueOnError)
	app := fs.String("app", "", "app slug, to resolve a vN revision (defaults to linked project app)")
	comment := fs.String("comment", "", "note recorded with the decision")
	reason := fs.String("reason", "", "why the release is rejected (required to reject)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale release approval "+decision+" <deployment> [--app <slug>] [--comment TEXT | --reason TEXT]", "approvals")
		return 1
	}
	note := strings.TrimSpace(*comment)
	if decision == api.ReleaseApprovalDecisionReject && strings.TrimSpace(*reason) != "" {
		note = strings.TrimSpace(*reason)
	}
	req := api.DecideReleaseApprovalRequest{Decision: decision, Comment: note}
	if err := req.Validate(); err != nil {
		return printErr("Invalid decision", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	id, err := resolveDeploymentArg(context.Background(), client, *app, fs.Arg(0))
	if err != nil {
		return printErr("Could not resolve the deployment", err)
	}
	status, err := client.DecideReleaseApproval(context.Background(), id, req)
	if err != nil {
		return printErr("Could not record the decision", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(status))
	}
	printApprovalStatus(status)
	return 0
}

func printApprovalStatus(s api.ReleaseApprovalStatus) {
	switch s.State {
	case api.ReleaseApprovalStateNotRequired:
		_, _ = fmt.Fprintf(osStdout, "Release %s of %s does not need approval.\n", s.DeploymentID, s.App)
		return
	case api.ReleaseApprovalStateApproved:
		_, _ = fmt.Fprintf(osStdout, "Release %s of %s is approved (%d/%d); the rollout continues on its next step.\n", s.DeploymentID, s.App, s.Approvals, s.RequiredApprovals)
	case api.ReleaseApprovalStateRejected:
		_, _ = fmt.Fprintf(osStdout, "Release %s of %s was rejected; its rollout is aborted.\n", s.DeploymentID, s.App)
	case api.ReleaseApprovalStateExpired:
		_, _ = fmt.Fprintf(osStdout, "Release %s of %s waited past its approval timeout and is aborted.\n", s.DeploymentID, s.App)
	default:
		_, _ = fmt.Fprintf(osStdout, "Release %s of %s needs %d more approval(s) (%d/%d).\n", s.DeploymentID, s.App, s.Remaining(), s.Approvals, s.RequiredApprovals)
		if s.ExpiresAt != "" {
			_, _ = fmt.Fprintf(osStdout, "It waits in canary stage %d until %s.\n", s.AfterStage, s.ExpiresAt)
		}
	}
	for _, d := range s.Decisions {
		note := ""
		if d.Comment != "" {
			note = ": " + d.Comment
		}
		counted := ""
		if !d.Counted && d.Decision == api.ReleaseApprovalDecisionApprove {
			counted = " (not counted: the deployer)"
		}
		_, _ = fmt.Fprintf(osStdout, "  %s %s by %s (%s)%s%s\n", d.DecidedAt, d.Decision, d.Approver, d.Role, counted, note)
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
