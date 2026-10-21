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

func cmdProjectsEnvironmentGitOpsPromotionPR(args []string) int {
	flags, positional := splitArgsForFlags(args, "yes", "json")
	fs := newFlagSet("environment-gitops-promotion-pr", flag.ContinueOnError)
	from := fs.String("from", "", "source environment with a proven serving release")
	commit := fs.String("target-commit", "", "reviewed current target branch commit")
	expected := fs.String("expected-plan-hash", "", "reviewed promotion observation hash")
	yes := fs.Bool("yes", false, "create the draft proposal; default is a read-only preview")
	asJSON := fs.Bool("json", false, "emit the proposal receipt as JSON")
	if fs.Parse(flags) != nil || len(positional) != 2 || !validEnvironmentGitOpsTarget(positional) || !api.ValidProjectEnvironmentSlug(*from) || *from == positional[1] || !validGitOpsPlanCommit(*commit) || !validGitOpsPlanCommitHash(*expected) {
		PrintUsage(os.Stderr, environmentGitOpsUsage+" --from ENV --target-commit SHA --expected-plan-hash HASH [--yes] [--json]", "projects environments")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	request := api.PreviewEnvironmentGitPromotionRequest{FromEnvironment: *from, TargetCommitSHA: *commit, ExpectedPlanHash: *expected}
	fetch := func(ctx context.Context) (proposal.Request, error) {
		plan, err := client.PreviewEnvironmentGitPromotion(ctx, positional[0], positional[1], request)
		if err != nil {
			return proposal.Request{}, err
		}
		if plan.Project != positional[0] || plan.FromEnvironment != *from || plan.ToEnvironment != positional[1] || plan.Target.CommitSHA != *commit {
			return proposal.Request{}, fmt.Errorf("promotion response scope mismatch")
		}
		status, err := client.GetEnvironmentGitOps(ctx, positional[0], positional[1])
		return proposal.Request{Plan: plan, Source: status.Source, ExpectedPlanHash: *expected}, err
	}
	input, err := fetch(ctx)
	if err != nil {
		return printErr("GitOps promotion proposal failed", err)
	}
	provider, err := proposal.CLI(input.Plan.Origin.Provider, input.Source.Spec.Repository)
	if err != nil {
		return printErr("GitOps promotion proposal failed", err)
	}
	recheck := func(ctx context.Context) error {
		fresh, err := fetch(ctx)
		if err != nil {
			return err
		}
		return proposal.ValidateObservation(input, fresh)
	}
	out, err := proposal.Run(ctx, input, provider, *yes, recheck)
	if err != nil {
		return printErr("GitOps promotion proposal failed", err)
	}
	if *asJSON || jsonOutput {
		return environmentGitOpsOutput(out, nil)
	}
	if out.Status == "preview" {
		_, err = fmt.Fprintf(osStdout, "Preview: draft %s proposal for %s / %s → %s\nRepository: %s\nBase: %s @ %s\nBranch: %s\nPlan: %s\nUse --yes to create the proposal. Normal review and fresh target qualification remain required.\n", out.Provider, positional[0], *from, positional[1], out.Repository, out.Base, out.BaseCommit, out.Branch, out.PlanHash)
	} else {
		_, err = fmt.Fprintf(osStdout, "%s: %s\nPlan: %s\nNormal review and fresh target qualification remain required.\n", out.Status, out.URL, out.PlanHash)
	}
	if err != nil {
		return printErr("Could not write proposal receipt", err)
	}
	return 0
}

func validGitOpsPlanCommitHash(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	for _, c := range hash {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
