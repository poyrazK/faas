package main

import (
	"context"
	"errors"
	"flag"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingrun"
	"os"
	"os/signal"
	"sort"
	"time"
)

func createDurableProjectVerification(ctx context.Context, client *api.Client, manifest api.CheckedProjectReleaseManifest, id string, write, queue bool, interval, timeout time.Duration) int {
	if id == "" {
		id = uuid.NewString()
	}
	review, err := fetchProjectVerificationReview(ctx, client, manifest, write, queue)
	if err != nil {
		return printErr("Could not review graph verification", err)
	}
	if !review.Ready {
		return previewProjectVerification(ctx, client, manifest, write, queue)
	}
	ttl := min(int(timeout/time.Second), api.BindingVerificationRunMaxTTLSeconds)
	creationProgress := newBindingProgress(ctx, id, "", manifest.Project, manifest.Environment)
	options := bindingrun.CreationOptions{StatusReadRetries: bindingStatusReadRetries(ctx), OnProgress: creationProgress.observe, MaxConcurrency: bindingVerificationConcurrency(ctx), RunID: id, TTLSeconds: ttl, ObjectStorageWrite: write, QueuePublish: queue}
	announceBindingRun(ctx, id, "", manifest.Project, manifest.Environment)
	run, err := bindingrun.CreateProject(ctx, client, manifest.Project, manifest.Environment, review, options)
	if err != nil {
		return renderBindingCreationFailure(id, "", manifest.Project, manifest.Environment, err)
	}
	return executeDurableProjectVerification(ctx, client, run, interval, timeout)
}
func executeDurableProjectVerification(ctx context.Context, client *api.Client, run api.ProjectBindingVerificationRun, interval, timeout time.Duration) int {
	interrupted, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	until := time.Now().Add(timeout)
	if !run.ExpiresAt.IsZero() && run.ExpiresAt.Before(until) {
		until = run.ExpiresAt
	}
	bounded, cancel := context.WithDeadline(interrupted, until)
	defer cancel()
	progress := newBindingProgress(ctx, run.ID, "", run.Project, run.Environment)
	var runnerErr error
	defer func() {
		if bounded.Err() != nil || errors.Is(runnerErr, context.Canceled) || errors.Is(runnerErr, context.DeadlineExceeded) {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), bindingProbeCancelTimeout)
			defer stop()
			if _, err := client.CancelProjectBindingVerificationRun(cleanup, run.Project, run.Environment, run.ID); err != nil {
				if progress.mode == "ndjson" {
					progress.notice("cleanup_failed", progress.recovery())
				} else {
					PrintWarn(osStderr, "Could not cancel graph run %s: %v; retry verification-runs cancel", run.ID, err)
				}
			}
		}
	}()
	observed, err := bindingrun.RunProject(bounded, client, run.Project, run.Environment, run.ID, bindingrun.Options{OnProgress: progress.observe, StatusReadRetries: bindingStatusReadRetries(ctx), PollInterval: interval, Concurrency: bindingVerificationConcurrency(ctx)})
	runnerErr = err
	if observed.ID != "" {
		run = observed
	}
	code := 0
	if err != nil {
		code = 1
		run.Reason = err.Error()
		if errors.Is(err, context.Canceled) {
			code = 130
		}
	}
	var diagnostics *api.ProjectBindingVerificationRunDiagnostics
	var recovery *bindingrun.RecoveryError
	if errors.As(err, &recovery) {
		diagnostics = recovery.ProjectDiagnostics
	}
	names := make([]string, 0, len(run.Members))
	results := map[string]bindingPolicyVerificationReport{}
	for name, member := range run.Members {
		names = append(names, name)
		report, _ := reportFromDurableRun(member, err)
		if diagnostics != nil {
			if d, ok := diagnostics.Members[name]; ok {
				report.Diagnostics = &d
			}
		}
		results[name] = report
	}
	sort.Strings(names)
	var summary *bindingRecoverySummary
	if err != nil {
		summary = progress.recovery()
	}
	if jsonOutput {
		returnCode := jsonOut(writeJSON(struct {
			Recovery    *bindingRecoverySummary                       `json:"recovery,omitempty"`
			Diagnostics *api.ProjectBindingVerificationRunDiagnostics `json:"diagnostics,omitempty"`
			Run         api.ProjectBindingVerificationRun             `json:"run"`
			Passed      bool                                          `json:"passed"`
			Reports     map[string]bindingPolicyVerificationReport    `json:"reports"`
		}{summary, diagnostics, run, code == 0, results}))
		if returnCode != 0 {
			return returnCode
		}
	} else {
		PrintOK(osStdout, "Graph verification run %s: %s", run.ID, run.Status)
		for _, name := range names {
			view := results[name]
			view.Diagnostics = nil
			progress.renderReport(view)
		}
		if diagnostics != nil && progress.mode != "ndjson" {
			renderAutomaticDiagnostics(diagnostics.Blockers, diagnostics.Recovery)
		}
		if progress.mode == "ndjson" {
			if summary != nil {
				progress.notice("verification_run_stopped", summary)
			}
		} else {
			renderBindingRecovery(summary)
		}
		if run.Reason != "" && progress.mode != "ndjson" {
			PrintWarn(osStderr, "%s", run.Reason)
		}
	}
	return code
}
func cmdProjectsVerificationRuns(args []string) int {
	if len(args) > 0 && args[0] == "list" {
		return cmdBindingVerificationRunList(args[1:], true)
	}
	if len(args) == 0 {
		return printErr("Missing run action", errors.New("use status, diagnostics, resume or cancel"))
	}
	action := args[0]
	flags, pos := splitArgsForFlags(args[1:])
	fs := newFlagSet("project-verification-runs", flag.ContinueOnError)
	progressMode := fs.String("progress", "auto", "durable progress on stderr: auto, plain, ndjson or off")
	statusRetries := fs.Int("status-read-retries", api.BindingVerificationRunnerDefaultStatusReadRetries, "transient durable status GET retries (0–8)")
	concurrency := fs.Int("concurrency", bindingVerificationConcurrencyDefault, "shared graph probe limit (1–8)")
	interval := fs.Duration("poll-interval", executionPollIntervalDefault, "polling interval")
	timeout := fs.Duration("wait-timeout", bindingProbeWaitTimeoutDefault, "remaining graph wait budget")
	if err := fs.Parse(flags); err != nil || fs.NArg() != 0 || len(pos) != 3 || !api.ValidProjectSlug(pos[0]) || !api.ValidProjectEnvironmentSlug(pos[1]) || *statusRetries < 0 || *statusRetries > api.BindingVerificationRunnerMaxStatusReadRetries || *concurrency < 1 || *concurrency > bindingVerificationConcurrencyMax || *interval <= 0 || *timeout <= 0 {
		return printErr("Invalid graph run options", errors.New("use verification-runs <status|diagnostics|resume|cancel> PROJECT ENVIRONMENT RUN_ID"))
	}
	if !validBindingProgress(*progressMode) {
		return printErr("Invalid progress option", errors.New("use --progress auto|plain|ndjson|off"))
	}
	id, err := uuid.Parse(pos[2])
	if err != nil || id == uuid.Nil {
		return printErr("Invalid graph run identity", errors.New("run ID must be a UUID"))
	}
	executionFlags := false
	fs.Visit(func(f *flag.Flag) {
		executionFlags = executionFlags || f.Name == "concurrency" || f.Name == "status-read-retries" || f.Name == "progress" || f.Name == "poll-interval" || f.Name == "wait-timeout"
	})
	if action != "status" && action != "diagnostics" && action != "resume" && action != "cancel" || action != "resume" && executionFlags {
		return printErr("Invalid run action", errors.New("execution flags require resume"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(withBindingProgress(withBindingStatusReadRetries(withBindingVerificationBudget(context.Background(), *concurrency), statusRetries), *progressMode), *timeout)
	defer cancel()
	if action == "diagnostics" {
		return showProjectRunDiagnostics(ctx, client, pos[0], pos[1], id.String())
	}
	var run api.ProjectBindingVerificationRun
	switch action {
	case "status":
		run, err = client.GetProjectBindingVerificationRun(ctx, pos[0], pos[1], id.String())
	case "cancel":
		run, err = client.CancelProjectBindingVerificationRun(ctx, pos[0], pos[1], id.String())
	case "resume":
		return executeDurableProjectVerification(ctx, client, api.ProjectBindingVerificationRun{ID: id.String(), Project: pos[0], Environment: pos[1], Members: map[string]api.BindingVerificationRun{}}, *interval, *timeout)
	}
	if err != nil {
		return printErr("Graph verification run unavailable", err)
	}
	if run.ID != id.String() || run.Project != pos[0] || run.Environment != pos[1] {
		return printErr("Unconfirmed graph run", errors.New("server returned another selection"))
	}
	return jsonOut(writeJSON(run))
}
