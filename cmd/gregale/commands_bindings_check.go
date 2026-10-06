// adr: 427 — read-only bindings preflight exposes deterministic CI blockers.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
)

func cmdBindingsCheck(args []string) int {
	fs := newFlagSet("bindings check", flag.ContinueOnError)
	scope := fs.String("scope", "", "require the selected deployment to use this scope (default its current scope)")
	deployment := fs.String("deployment", "", "exact live deployment id or vN revision whose evidence must pass")
	maxAge := fs.Duration("max-verification-age", bindingcheck.DefaultMaxVerificationAge, "maximum age of passed probe evidence (default 10m)")
	allowUnsupported := fs.Bool("allow-unsupported", false, "waive connectivity coverage for active queue and outbound bindings")
	requireAck := fs.Bool("require-application-ack", false, "require current application acknowledgements for PostgreSQL and object-storage binding secrets")
	wait := fs.Bool("wait", false, "poll read-only inventory while probes, refreshes or application acknowledgements are pending")
	timeout := fs.Duration("timeout", bindingProbeWaitTimeoutDefault, "maximum time to wait for binding preflight")
	pollInterval := fs.Duration("poll-interval", bindingCheckPollIntervalDefault, "inventory polling interval with --wait")
	flags, positionals := splitArgsForFlags(args, "allow-unsupported", "require-application-ack", "wait")
	if err := fs.Parse(flags); err != nil || fs.NArg() != 0 || len(positionals) != 1 || !api.ValidAppSlug(strings.TrimSpace(positionals[0])) || *maxAge <= 0 || *timeout <= 0 || *pollInterval <= 0 || !validBindingDeploymentFlag(*deployment) {
		PrintUsage(osStderr, "usage: gregale bindings check <app> [--deployment ID|vN] [--scope SCOPE] [--max-verification-age DURATION] [--allow-unsupported] [--require-application-ack] [--wait --timeout DURATION --poll-interval DURATION] [--json]", "bindings")
		return 1
	}
	if *scope != "" {
		if problem := api.ValidateScope(*scope); problem != nil {
			return printErr("Invalid binding scope", problem)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if *wait {
		interrupted, stop := signal.NotifyContext(ctx, os.Interrupt)
		defer stop()
		bounded, cancel := context.WithTimeout(interrupted, *timeout)
		defer cancel()
		ctx = bounded
	}
	slug := strings.TrimSpace(positionals[0])
	deploymentID, err := resolveBindingDeployment(ctx, client, slug, *deployment)
	if err != nil {
		code := printErr("Could not resolve binding check deployment", err)
		if errors.Is(err, context.Canceled) {
			return 130
		}
		return code
	}
	policy := bindingcheck.Policy{App: slug, Scope: *scope, DeploymentID: deploymentID, MaxVerificationAge: *maxAge, AllowUnsupported: *allowUnsupported, RequireApplicationAck: *requireAck}
	report, err := pollBindingCheck(ctx, client, policy, *wait, *pollInterval)
	if err != nil && report.App == "" {
		code := printErr("Could not load app bindings", err)
		if errors.Is(err, context.Canceled) {
			return 130
		}
		return code
	}
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "Binding preflight wait ended:", err)
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		renderBindingCheck(report)
	}
	if errors.Is(err, context.Canceled) {
		return 130
	}
	if err != nil || !report.Passed {
		return 1
	}
	return 0
}

func renderBindingCheck(report bindingcheck.Report) {
	status := "passed"
	if !report.Passed {
		status = "blocked"
	}
	_, _ = fmt.Fprintf(osStdout, "Bindings preflight %s for %s: scope=%s deployment=%s coverage=%s max-verification-age=%s\n",
		status, report.App, humanBindingValue(report.Scope), humanBindingValue(report.DeploymentID), report.Coverage, report.MaxVerificationAge)
	for _, item := range report.Bindings {
		_, _ = fmt.Fprintf(osStdout, "%s %s (%s) scope=%s: %s", item.Type, item.Name, humanBindingValue(item.Binding), item.Scope, item.Status)
		if item.Reason != "" {
			_, _ = fmt.Fprintf(osStdout, " (%s)", item.Reason)
		}
		_, _ = fmt.Fprintln(osStdout)
		if adoption := item.ApplicationAdoption; adoption != nil {
			_, _ = fmt.Fprintf(osStdout, "  application adoption=%s; ACK pairs current=%d failed=%d stale=%d unknown=%d; reload pairs current=%d failed=%d stale=%d unknown=%d\n", adoption.Status, adoption.Application.Current, adoption.Application.Failed, adoption.Application.Stale, adoption.Application.Unknown, adoption.Reload.Current, adoption.Reload.Failed, adoption.Reload.Stale, adoption.Reload.Unknown)
			for _, target := range adoption.Targets {
				if target.ReloadStatus == "current" && target.ApplicationAckStatus == "current" {
					continue
				}
				workload := target.WorkloadName
				if workload == "" {
					workload = "main"
				}
				_, _ = fmt.Fprintf(osStdout, "  target deployment=%s instance=%s workload=%s secret=%s: reload=%s (%s); application_ack=%s (%s)\n",
					target.DeploymentID, target.InstanceID, workload, target.Key,
					target.ReloadStatus, target.ReloadReason, target.ApplicationAckStatus, target.ApplicationAckReason)
			}
		}
	}
	for _, deployment := range report.Runtime {
		_, _ = fmt.Fprintf(osStdout, "runtime %s scope=%s deployment=%s: serving current=%d stale=%d unknown=%d; resident current=%d stale=%d unknown=%d; starting=%d\n",
			deployment.Status, deployment.Scope, deployment.DeploymentID, deployment.Serving.Current, deployment.Serving.Stale, deployment.Serving.Unknown,
			deployment.Resident.Current, deployment.Resident.Stale, deployment.Resident.Unknown, deployment.Starting)
	}
	for _, issue := range report.Issues {
		_, _ = fmt.Fprintf(osStdout, "Inventory issue %s/%s: %s\n", issue.Type, issue.Code, issue.Message)
	}
	for _, blocker := range report.Blockers {
		renderBindingCheckFinding("Blocker", blocker)
	}
	for _, warning := range report.Warnings {
		renderBindingCheckFinding("Warning", warning)
	}
	_, _ = fmt.Fprintln(osStdout, "This checks recorded probe evidence and instance timestamps. Application acknowledgements, when required, are version-bound self-attestations. It does not confirm resident credential use or application readiness; object-storage evidence covers bucket-list read access only.")
}

func renderBindingCheckFinding(label string, finding bindingcheck.Finding) {
	_, _ = fmt.Fprintf(osStdout, "%s %s", label, finding.Code)
	if finding.Type != "" {
		_, _ = fmt.Fprintf(osStdout, " type=%s name=%s binding=%s", finding.Type, finding.Name, humanBindingValue(finding.Binding))
	}
	if finding.Scope != "" {
		_, _ = fmt.Fprintf(osStdout, " scope=%s", finding.Scope)
	}
	if finding.DeploymentID != "" {
		_, _ = fmt.Fprintf(osStdout, " deployment=%s", finding.DeploymentID)
	}
	_, _ = fmt.Fprintf(osStdout, ": %s\n", finding.Message)
}
