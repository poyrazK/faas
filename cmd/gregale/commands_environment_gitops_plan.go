package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEnvironmentGitOpsPlan(args []string) int {
	flags, target := splitArgsForFlags(args, "fail-on-blocked")
	fs := newFlagSet("environment-gitops-plan", flag.ContinueOnError)
	commit := fs.String("commit", "", "immutable lowercase Git commit SHA")
	format := fs.String("format", "json", "json or markdown")
	fail := fs.Bool("fail-on-blocked", false, "exit 2 after writing a blocked plan")
	if fs.Parse(flags) != nil || !validEnvironmentGitOpsTarget(target) || !validGitOpsPlanCommit(*commit) || (*format != "json" && *format != "markdown") {
		PrintUsage(os.Stderr, environmentGitOpsUsage+" --commit SHA [--format json|markdown] [--fail-on-blocked]", "projects environments")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	preview, err := client.PreviewEnvironmentGitPlan(context.Background(), target[0], target[1], api.PreviewEnvironmentGitRevisionRequest{CommitSHA: *commit})
	if err != nil {
		return printErr("Environment GitOps plan failed", err)
	}
	if !validGitOpsPlanResponse(preview, target[0], target[1], *commit) {
		return printErr("Environment GitOps plan failed", fmt.Errorf("the server returned an unverified or unredacted candidate preview"))
	}
	if *format == "markdown" {
		if err := writeGitOpsPlanMarkdown(osStdout, preview); err != nil {
			return printErr("Could not write plan", err)
		}
	} else if code := environmentGitOpsOutput(preview, nil); code != 0 {
		return code
	}
	if *fail && !preview.Plan.CanApply() {
		return 2
	}
	return 0
}

func validGitOpsPlanResponse(preview api.PreviewEnvironmentGitPlanResponse, project, environment, commit string) bool {
	if !preview.ReadOnly || !preview.ValuesRedacted || preview.Project != project || preview.Environment != environment || preview.Plan.CommitSHA != commit ||
		preview.SourceID == "" || preview.Plan.Manager != preview.SourceID || preview.Plan.Revision != "candidate:"+commit || len(preview.Plan.Hash) != 64 || len(preview.Plan.DesiredDigest) != 64 ||
		preview.Plan.Generation < 0 || preview.Plan.ObservedVersion < 0 {
		return false
	}
	for _, change := range preview.Plan.Changes {
		if len(change.Before) != 0 || len(change.After) != 0 {
			return false
		}
	}
	return true
}

func validGitOpsPlanCommit(commit string) bool {
	if len(commit) != 40 {
		return false
	}
	for _, c := range commit {
		if (c < 'a' || c > 'f') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func gitOpsMarkdownText(value string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "|", "\\|", "`", "&#96;", "\r", " ", "\n", " ").Replace(value)
}

func writeGitOpsPlanMarkdown(w io.Writer, preview api.PreviewEnvironmentGitPlanResponse) error {
	plan := preview.Plan
	var out strings.Builder
	fmt.Fprintf(&out, "### Gregale GitOps plan: %s / %s\n\n", gitOpsMarkdownText(preview.Project), gitOpsMarkdownText(preview.Environment))
	fmt.Fprintf(&out, "Commit `%s` · source generation %d · observed intent %d\n\n", gitOpsMarkdownText(plan.CommitSHA), plan.Generation, plan.ObservedVersion)
	out.WriteString("Read-only preview; values are redacted. This result does not approve, adopt, deploy, or certify runtime readiness. Re-run after source or environment changes.\n\n")
	if !plan.CanApply() {
		out.WriteString("**Blocked:**\n\n")
		for _, reason := range plan.BlockingReasons {
			fmt.Fprintf(&out, "- %s\n", gitOpsMarkdownText(reason))
		}
		out.WriteString("\n")
	}
	out.WriteString("| Resource | Field | Action | Reason |\n| --- | --- | --- | --- |\n")
	for _, change := range plan.Changes {
		if change.Action == "keep" || change.Action == "retain_unmanaged" {
			continue
		}
		fmt.Fprintf(&out, "| %s | %s | %s | %s |\n", gitOpsMarkdownText(change.Resource), gitOpsMarkdownText(change.Path), gitOpsMarkdownText(change.Action), gitOpsMarkdownText(change.Reason))
	}
	_, err := io.WriteString(w, out.String())
	return err
}
