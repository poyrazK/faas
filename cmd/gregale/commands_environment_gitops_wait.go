package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops/rollout"
)

func cmdProjectsEnvironmentGitOpsWait(args []string) int {
	flags, target := splitArgsForFlags(args, "json")
	fs := newFlagSet("environment-gitops-wait", flag.ContinueOnError)
	commit := fs.String("commit", "", "exact merged Git commit to wait for")
	timeout := fs.Duration("timeout", 15*time.Minute, "overall wait deadline, at most 24h")
	interval := fs.Duration("poll-interval", 2*time.Second, "delay between status reads, from 1s to 1m")
	asJSON := fs.Bool("json", false, "emit one final read-only rollout receipt as JSON")
	if fs.Parse(flags) != nil || !validEnvironmentGitOpsTarget(target) || !validGitOpsPlanCommit(*commit) || *timeout <= 0 || *timeout > 24*time.Hour || *interval < time.Second || *interval > time.Minute {
		PrintUsage(os.Stderr, environmentGitOpsUsage+" --commit SHA [--timeout 15m] [--poll-interval 2s] [--json]", "projects environments")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, *timeout)
	defer cancel()
	return runEnvironmentGitOpsWait(ctx, client, target[0], target[1], *commit, *interval, *asJSON || jsonOutput)
}

func runEnvironmentGitOpsWait(ctx context.Context, client *api.Client, project, environment, commit string, interval time.Duration, asJSON bool) int {
	tracker, err := rollout.NewTracker(commit, environment)
	if err != nil {
		return printErr("GitOps wait failed", err)
	}
	previous := ""
	progress := func(out rollout.Result) error {
		if asJSON {
			return nil
		}
		// Clock churn must not repeat unchanged progress. JSON output is final only.
		comparable := out
		comparable.ObservedAt = time.Time{}
		if out.Explanation != nil {
			copy := *out.Explanation
			copy.AssessedAt = time.Time{}
			comparable.Explanation = &copy
		}
		encoded, err := json.Marshal(comparable)
		if err != nil || string(encoded) == previous {
			return err
		}
		previous = string(encoded)
		if _, err := fmt.Fprintf(osStderr, "GitOps wait: %s (%s)\n", out.Status, out.Reason); err != nil {
			return err
		}
		if out.Explanation != nil {
			return writeEnvironmentGitOpsExplanation(osStderr, *out.Explanation)
		}
		return nil
	}
	out, err := rollout.Wait(ctx, tracker, func(ctx context.Context) (api.EnvironmentGitOpsStatusResponse, error) {
		return client.GetEnvironmentGitOps(ctx, project, environment)
	}, interval, progress)
	if err != nil {
		return printErr("GitOps wait failed", err)
	}
	if asJSON {
		if code := environmentGitOpsOutput(out, nil); code != 0 {
			return code
		}
	} else {
		if _, err := fmt.Fprintf(osStdout, "GitOps %s: %s / %s @ %s (%s)\n", out.Status, project, environment, commit, out.Reason); err != nil {
			return printErr("Could not write rollout receipt", err)
		}
		if out.ExitCode() != 0 {
			_, _ = fmt.Fprintf(osStderr, "Inspect current recorded evidence: gregale projects environments gitops explain %s %s\n", project, environment)
		}
	}
	return out.ExitCode()
}
