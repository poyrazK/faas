package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops/proposal"
)

func cmdProjectsEnvironmentGitOpsRollbackPR(args []string) int {
	flags, positional := splitArgsForFlags(args, "yes", "json")
	fs := newFlagSet("environment-gitops-rollback-pr", flag.ContinueOnError)
	release := fs.String("to-release", "", "explicit retained serving release in this environment")
	commit := fs.String("target-commit", "", "reviewed current target branch commit")
	expected := fs.String("expected-plan-hash", "", "reviewed rollback observation hash")
	yes := fs.Bool("yes", false, "create the draft recovery proposal; default is a read-only preview")
	asJSON := fs.Bool("json", false, "emit the recovery proposal receipt as JSON")
	if fs.Parse(flags) != nil || len(positional) != 2 || !validEnvironmentGitOpsTarget(positional) || !validGitOpsRollbackRelease(*release) || !validGitOpsPlanCommit(*commit) || !validGitOpsPlanCommitHash(*expected) {
		PrintUsage(os.Stderr, environmentGitOpsUsage+" --to-release UUID --target-commit SHA --expected-plan-hash HASH [--yes] [--json]", "projects environments")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	request := api.PreviewEnvironmentGitRollbackRequest{ReleaseSetID: *release, TargetCommitSHA: *commit, ExpectedPlanHash: *expected}
	fetch := func(ctx context.Context) (proposal.RollbackRequest, error) {
		plan, err := client.PreviewEnvironmentGitRollback(ctx, positional[0], positional[1], request)
		if err != nil {
			return proposal.RollbackRequest{}, err
		}
		if plan.Project != positional[0] || plan.Environment != positional[1] || plan.Origin.ReleaseSetID != *release || plan.Target.CommitSHA != *commit {
			return proposal.RollbackRequest{}, fmt.Errorf("rollback response scope or selected release mismatch")
		}
		status, err := client.GetEnvironmentGitOps(ctx, positional[0], positional[1])
		return proposal.RollbackRequest{Plan: plan, Source: status.Source, ReleaseSetID: *release, ExpectedPlanHash: *expected}, err
	}
	input, err := fetch(ctx)
	if err != nil {
		return printErr("GitOps rollback proposal failed", err)
	}
	provider, err := proposal.CLI(input.Plan.Origin.Provider, input.Source.Spec.Repository)
	if err != nil {
		return printErr("GitOps rollback proposal failed", err)
	}
	recheck := func(ctx context.Context) error {
		fresh, err := fetch(ctx)
		if err != nil {
			return err
		}
		return proposal.ValidateRollbackObservation(input, fresh)
	}
	out, err := proposal.RunRollback(ctx, input, provider, *yes, recheck)
	if err != nil {
		return printErr("GitOps rollback proposal failed", err)
	}
	if *asJSON || jsonOutput {
		return environmentGitOpsOutput(out, nil)
	}
	if out.Status == "preview" {
		_, err = fmt.Fprintf(osStdout, "Preview: draft %s recovery proposal for %s / %s\nRelease: %s\nRepository: %s\nBase: %s @ %s\nBranch: %s\nPlan: %s\nUse --yes to create the proposal. Normal review and fresh target qualification remain required. Code recovery does not restore data or replay completed Jobs.\n", out.Provider, positional[0], positional[1], out.ReleaseSetID, out.Repository, out.Base, out.BaseCommit, out.Branch, out.PlanHash)
	} else {
		_, err = fmt.Fprintf(osStdout, "%s: %s\nRelease: %s\nPlan: %s\nNormal review and fresh target qualification remain required. Code recovery does not restore data or replay completed Jobs.\n", out.Status, out.URL, out.ReleaseSetID, out.PlanHash)
	}
	if err != nil {
		return printErr("Could not write recovery proposal receipt", err)
	}
	return 0
}
