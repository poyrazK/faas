package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const environmentGitOpsUsage = "usage: gregale projects environments gitops <status|bind|review|approve|adoption-preview|adopt|controls|override|remove-override> <project> <environment> [flags]"

func cmdProjectsEnvironmentGitOps(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, environmentGitOpsUsage, "projects environments")
		return 1
	}
	switch args[0] {
	case "status", "adoption-preview":
		return cmdEnvironmentGitOpsRead(args[0], args[1:])
	case "bind":
		return cmdEnvironmentGitOpsBind(args[1:])
	case "review":
		return cmdEnvironmentGitOpsReview(args[1:])
	case "approve", "adopt":
		return cmdEnvironmentGitOpsReviewedAction(args[0], args[1:])
	case "controls":
		return cmdEnvironmentGitOpsControls(args[1:])
	case "override", "remove-override":
		return cmdEnvironmentGitOpsOverride(args[0], args[1:])
	default:
		PrintUsage(os.Stderr, environmentGitOpsUsage, "projects environments")
		return 1
	}
}

func validEnvironmentGitOpsTarget(positional []string) bool {
	return len(positional) == 2 && api.ValidProjectSlug(positional[0]) && api.ValidProjectEnvironmentSlug(positional[1])
}

func cmdEnvironmentGitOpsRead(action string, args []string) int {
	if !validEnvironmentGitOpsTarget(args) {
		PrintUsage(os.Stderr, environmentGitOpsUsage, "projects environments")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if action == "adoption-preview" {
		plan, err := client.PreviewEnvironmentGitOpsAdoption(context.Background(), args[0], args[1])
		return environmentGitOpsOutput(plan, err)
	}
	status, err := client.GetEnvironmentGitOps(context.Background(), args[0], args[1])
	return environmentGitOpsOutput(status, err)
}

func environmentGitOpsOutput(result any, err error) int {
	if err != nil {
		return printErr("Environment GitOps request failed", err)
	}
	// Review and adoption receipts must preserve the hash and generation for
	// a subsequent explicit action. All commands emit portable JSON.
	return jsonOut(writeJSON(result))
}

func cmdEnvironmentGitOpsBind(args []string) int {
	flags, positional := splitArgsForFlags(args, "prune")
	fs := newFlagSet("environment-gitops-bind", flag.ContinueOnError)
	manifest := fs.String("manifest-path", "", "Git path to the environment definition")
	ref := fs.String("ref", "", "Git ref (defaults to the project production branch)")
	mode := fs.String("mode", "report", "report or enforce")
	approval := fs.String("approval-policy", "manual", "manual or protected_branch")
	prune := fs.Bool("prune", false, "allow removal of previously owned fields")
	if fs.Parse(flags) != nil || len(positional) != 2 || !validEnvironmentGitOpsTarget(positional) || *manifest == "" || (*mode != "report" && *mode != "enforce") || (*approval != "manual" && *approval != "protected_branch") {
		PrintUsage(os.Stderr, environmentGitOpsUsage+" --manifest-path PATH [--ref REF] [--mode report|enforce] [--approval-policy manual|protected_branch] [--prune]", "projects environments")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	source, err := client.CreateEnvironmentGitSource(context.Background(), positional[0], positional[1], api.CreateEnvironmentGitSourceRequest{
		Ref: *ref, ManifestPath: *manifest, Mode: *mode, ApprovalPolicy: *approval, Prune: *prune,
	})
	return environmentGitOpsOutput(source, err)
}

func cmdEnvironmentGitOpsReview(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("environment-gitops-review", flag.ContinueOnError)
	commit := fs.String("commit", "", "immutable lowercase GitHub commit SHA")
	if fs.Parse(flags) != nil || len(positional) != 2 || !validEnvironmentGitOpsTarget(positional) || len(*commit) != 40 {
		PrintUsage(os.Stderr, environmentGitOpsUsage+" --commit SHA", "projects environments")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	review, err := client.PreviewEnvironmentGitRevision(context.Background(), positional[0], positional[1], api.PreviewEnvironmentGitRevisionRequest{CommitSHA: *commit})
	return environmentGitOpsOutput(review, err)
}

func readEnvironmentGitOpsReceipt(path string, out any) error {
	file, err := openCustomerFile(path)
	if err != nil {
		return err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, api.EnvironmentGitOpsMaxDefinitionBytes*2+1))
	if err != nil || len(raw) > api.EnvironmentGitOpsMaxDefinitionBytes*2 {
		return errors.New("could not read a bounded GitOps review receipt")
	}
	return json.Unmarshal(raw, out)
}

func cmdEnvironmentGitOpsReviewedAction(action string, args []string) int {
	flags, positional := splitArgsForFlags(args, "yes")
	fs := newFlagSet("environment-gitops-"+action, flag.ContinueOnError)
	file := fs.String("file", "", "review JSON from review or adoption-preview")
	yes := fs.Bool("yes", false, "confirm the reviewed digest or ownership plan")
	if fs.Parse(flags) != nil || len(positional) != 2 || !validEnvironmentGitOpsTarget(positional) || *file == "" || !*yes {
		PrintUsage(os.Stderr, environmentGitOpsUsage+" --file REVIEW.json --yes", "projects environments")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if action == "adopt" {
		var plan api.EnvironmentGitOpsPlan
		if err := readEnvironmentGitOpsReceipt(*file, &plan); err != nil {
			return printErr("Could not read adoption plan", err)
		}
		if len(plan.Hash) != 64 || !plan.CanApply() {
			return printErr("Adoption is blocked", fmt.Errorf("review a current adoption-preview with no blocking reasons"))
		}
		out, err := client.AdoptEnvironmentGitOps(context.Background(), positional[0], positional[1], api.AdoptEnvironmentGitOpsRequest{PlanHash: plan.Hash})
		return environmentGitOpsOutput(out, err)
	}
	var review api.PreviewEnvironmentGitRevisionResponse
	if err := readEnvironmentGitOpsReceipt(*file, &review); err != nil {
		return printErr("Could not read revision review", err)
	}
	if len(review.CommitSHA) != 40 || len(review.DefinitionDigest) != 64 || review.Generation < 0 {
		return printErr("Incomplete revision review", errors.New("review must include commit_sha, definition_digest, and generation"))
	}
	out, err := client.ApproveEnvironmentGitRevision(context.Background(), positional[0], positional[1], api.ApproveEnvironmentGitRevisionRequest{CommitSHA: review.CommitSHA, DefinitionDigest: review.DefinitionDigest, ExpectedGeneration: review.Generation})
	return environmentGitOpsOutput(out, err)
}

func cmdEnvironmentGitOpsControls(args []string) int {
	flags, positional := splitArgsForFlags(args, "prune", "suspended")
	fs := newFlagSet("environment-gitops-controls", flag.ContinueOnError)
	generation := fs.Int64("generation", -1, "current source generation from status")
	mode := fs.String("mode", "", "report or enforce")
	prune := fs.Bool("prune", false, "allow removal of previously owned fields")
	suspended := fs.Bool("suspended", false, "pause reconciliation while retaining ownership")
	if fs.Parse(flags) != nil || len(positional) != 2 || !validEnvironmentGitOpsTarget(positional) || *generation < 0 || (*mode != "" && *mode != "report" && *mode != "enforce") {
		PrintUsage(os.Stderr, environmentGitOpsUsage+" --generation N [--mode report|enforce] [--prune=BOOL] [--suspended=BOOL]", "projects environments")
		return 1
	}
	request := api.EnvironmentGitSourceUpdate{ExpectedGeneration: *generation, Mode: *mode}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "prune":
			request.Prune = prune
		case "suspended":
			request.Suspended = suspended
		}
	})
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.UpdateEnvironmentGitSource(context.Background(), positional[0], positional[1], request)
	return environmentGitOpsOutput(out, err)
}

func cmdEnvironmentGitOpsOverride(action string, args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("environment-gitops-"+action, flag.ContinueOnError)
	resource := fs.String("resource", "", "logical resource, for example workload/api")
	path := fs.String("path", "", "owned field, for example variables/MODE")
	reason := fs.String("reason", "", "reason for the temporary edit")
	expires := fs.String("expires", "", "RFC3339 expiry within twenty-four hours")
	if fs.Parse(flags) != nil || len(positional) != 2 || !validEnvironmentGitOpsTarget(positional) || strings.TrimSpace(*resource) == "" || strings.TrimSpace(*path) == "" {
		PrintUsage(os.Stderr, environmentGitOpsUsage+" --resource RESOURCE --path PATH [--reason REASON --expires RFC3339]", "projects environments")
		return 1
	}
	var expiry time.Time
	if action == "override" {
		var err error
		expiry, err = time.Parse(time.RFC3339, *expires)
		if err != nil || strings.TrimSpace(*reason) == "" {
			return printErr("Invalid override", errors.New("reason and an RFC3339 expiry are required"))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if action == "remove-override" {
		err := client.RemoveEnvironmentGitOpsOverride(context.Background(), positional[0], positional[1], api.RemoveEnvironmentGitOpsOverrideRequest{Resource: *resource, Path: *path})
		return environmentGitOpsOutput(map[string]string{"status": "override_removed"}, err)
	}
	out, err := client.CreateEnvironmentGitOpsOverride(context.Background(), positional[0], positional[1], api.EnvironmentGitOpsOverrideRequest{Resource: *resource, Path: *path, Reason: *reason, ExpiresAt: expiry})
	return environmentGitOpsOutput(out, err)
}
