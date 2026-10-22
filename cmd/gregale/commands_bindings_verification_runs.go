// adr:1355 — durable receipts survive CLI/process and HTTP response loss.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingrun"
	"os"
	"os/signal"
	"reflect"
	"slices"
	"time"
)

type bindingVerificationRunKey struct{}
type bindingVerificationRunStepKey struct{}
type bindingVerificationRunStepContext struct {
	Run   *api.BindingVerificationRun
	Index int
}

func bindingProbeAutoCancel(ctx context.Context) bool {
	return ctx.Value(bindingVerificationRunKey{}) == nil
}
func admitVerificationRunStep(ctx context.Context, client *api.Client, app, kind string) (api.BindingVerificationRunStep, error) {
	selected, ok := ctx.Value(bindingVerificationRunStepKey{}).(bindingVerificationRunStepContext)
	if !ok {
		return api.BindingVerificationRunStep{}, fmt.Errorf("durable step context missing")
	}
	step, err := client.AdmitBindingVerificationRunStep(ctx, app, selected.Run.ID, selected.Index)
	if err != nil {
		return step, err
	}
	expected := selected.Run.Steps[selected.Index]
	id, parseErr := uuid.Parse(step.ResourceID)
	if parseErr != nil || id == uuid.Nil || id.String() != step.ResourceID || step.ResourceKind != kind || !step.Owned || !reflect.DeepEqual(step.Plan, expected.Plan) || expected.Owned && expected.ResourceID != "" && step.ResourceID != expected.ResourceID {
		return step, fmt.Errorf("server did not confirm the reviewed run step and resource identity")
	}
	return step, nil
}
func runDurableBindingVerification(ctx context.Context, client *api.Client, slug, ref, id string, dry, write, queue bool, interval, timeout time.Duration) int {
	bounded, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ctx = bounded
	if dry {
		return runBindingPolicyVerification(context.WithValue(ctx, queueCanaryConsentKey{}, queue), client, slug, ref, true, write, interval, timeout)
	}
	if id == "" {
		id = uuid.NewString()
	}
	review, err := fetchBindingVerificationReview(ctx, client, slug, ref, nil)
	if err != nil {
		return printErr("Could not review durable verification", err)
	}
	if !review.Ready {
		report, code := reportFromBindingVerificationReview(review, write)
		if jsonOutput {
			if output := jsonOut(writeJSON(report)); output != 0 {
				return output
			}
		} else {
			renderBindingPolicyVerification(report)
		}
		return code
	}
	// The run ID is recoverable even if the create response is lost.
	announceBindingRun(ctx, id, slug, "", "")
	ttl := int(timeout / time.Second)
	if ttl > api.BindingVerificationRunMaxTTLSeconds {
		ttl = api.BindingVerificationRunMaxTTLSeconds
	}
	if ttl < 1 {
		return printErr("Invalid verification deadline", fmt.Errorf("durable wait timeout must be at least one second"))
	}
	creationProgress := newBindingProgress(ctx, id, slug, "", "")
	run, err := bindingrun.CreateApp(ctx, client, slug, review, bindingrun.CreationOptions{StatusReadRetries: bindingStatusReadRetries(ctx), OnProgress: creationProgress.observe, MaxConcurrency: bindingVerificationConcurrency(ctx), RunID: id, TTLSeconds: ttl, ObjectStorageWrite: write, QueuePublish: queue})
	if err != nil {
		return renderBindingCreationFailure(id, slug, "", "", err)
	}
	return executeDurableBindingVerification(ctx, client, run, interval, timeout)
}
func executeDurableBindingVerification(ctx context.Context, client *api.Client, run api.BindingVerificationRun, interval, timeout time.Duration) int {
	interrupted, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	until := time.Now().Add(timeout)
	if !run.ExpiresAt.IsZero() && run.ExpiresAt.Before(until) {
		until = run.ExpiresAt
	}
	bounded, cancel := context.WithDeadline(interrupted, until)
	defer cancel()
	progress := newBindingProgress(ctx, run.ID, run.App, "", "")
	observed, err := bindingrun.RunApp(bounded, client, run.App, run.ID, bindingrun.Options{OnProgress: progress.observe, StatusReadRetries: bindingStatusReadRetries(ctx), PollInterval: interval, Concurrency: bindingVerificationConcurrency(ctx)})
	if observed.ID == "" {
		observed = run
	}
	report, code := reportFromDurableRun(observed, err)
	if err != nil {
		report.Recovery = progress.recovery()
	}
	if bounded.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), bindingProbeCancelTimeout)
		if _, err := client.CancelBindingVerificationRun(cleanup, run.App, run.ID); err != nil {
			if progress.mode == "ndjson" {
				progress.notice("cleanup_failed", progress.recovery())
			} else {
				PrintWarn(osStderr, "Could not cancel verification run %s: %v; use verification-runs cancel", run.ID, err)
			}
		}
		cancel()
	}
	if jsonOutput {
		if output := jsonOut(writeJSON(report)); output != 0 {
			return output
		}
	} else {
		progress.renderReport(report)
	}
	return code
}
func cmdBindingsVerificationRuns(args []string) int {
	if len(args) > 0 && args[0] == "list" {
		return cmdBindingVerificationRunList(args[1:], false)
	}
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("bindings-verification-runs", flag.ContinueOnError)
	interval := fs.Duration("poll-interval", executionPollIntervalDefault, "status polling interval")
	timeout := fs.Duration("wait-timeout", bindingProbeWaitTimeoutDefault, "maximum resume wait; original run deadline remains fixed")
	progressMode := fs.String("progress", "auto", "durable progress on stderr: auto, plain, ndjson or off")
	statusRetries := fs.Int("status-read-retries", api.BindingVerificationRunnerDefaultStatusReadRetries, "transient durable status GET retries (0–8)")
	concurrency := fs.Int("concurrency", bindingVerificationConcurrencyDefault, "maximum concurrent probes (1–8)")
	if err := fs.Parse(flags); err != nil || fs.NArg() != 0 || len(positional) != 3 || !slices.Contains([]string{"status", "diagnostics", "resume", "cancel"}, positional[0]) || !api.ValidAppSlug(positional[1]) || *interval <= 0 || *timeout <= 0 || *statusRetries < 0 || *statusRetries > api.BindingVerificationRunnerMaxStatusReadRetries || *concurrency < 1 || *concurrency > bindingVerificationConcurrencyMax {
		PrintUsage(osStderr, "usage: gregale bindings verification-runs <status|diagnostics|resume|cancel> <app> <run-id> [flags]", "bindings")
		return 1
	}
	if !validBindingProgress(*progressMode) {
		return printErr("Invalid progress option", errors.New("use --progress auto|plain|ndjson|off"))
	}
	id, err := uuid.Parse(positional[2])
	if err != nil || id == uuid.Nil {
		return printErr("Invalid verification run", errors.New("run ID must be a UUID"))
	}
	explicitExecution := false
	fs.Visit(func(f *flag.Flag) {
		explicitExecution = explicitExecution || f.Name == "poll-interval" || f.Name == "wait-timeout" || f.Name == "concurrency" || f.Name == "status-read-retries" || f.Name == "progress"
	})
	if positional[0] != "resume" && explicitExecution {
		return printErr("Invalid run options", errors.New("execution flags require resume"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := withBindingProgress(withBindingStatusReadRetries(withBindingVerificationBudget(context.Background(), *concurrency), statusRetries), *progressMode)
	if positional[0] == "diagnostics" {
		return showBindingRunDiagnostics(ctx, client, positional[1], id.String())
	}
	var run api.BindingVerificationRun
	switch positional[0] {
	case "status":
		run, err = client.GetBindingVerificationRun(ctx, positional[1], id.String())
	case "cancel":
		run, err = client.CancelBindingVerificationRun(ctx, positional[1], id.String())
	case "resume":
		return executeDurableBindingVerification(ctx, client, api.BindingVerificationRun{ID: id.String(), App: positional[1]}, *interval, *timeout)
	}
	if err != nil {
		return printErr("Verification run unavailable", err)
	}
	if run.ID != id.String() || run.App != positional[1] {
		return printErr("Invalid verification run", errors.New("server returned another run or app"))
	}
	if jsonOutput {
		return jsonOut(writeJSON(run))
	}
	_, _ = fmt.Fprintf(osStdout, "Verification run %s: %s (%s)\n", run.ID, run.Status, run.Reason)
	for i, step := range run.Steps {
		_, _ = fmt.Fprintf(osStdout, "%d %s %s: %s resource=%s owned=%t\n", i, step.Plan.Type, step.Plan.Binding, step.Status, step.ResourceID, step.Owned)
	}
	return 0
}
