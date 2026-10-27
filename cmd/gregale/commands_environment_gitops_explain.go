package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops/diagnostics"
)

func cmdProjectsEnvironmentGitOpsExplain(args []string) int {
	flags, target := splitArgsForFlags(args, "json")
	fs := newFlagSet("environment-gitops-explain", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit the read-only explanation as JSON")
	if fs.Parse(flags) != nil || !validEnvironmentGitOpsTarget(target) {
		PrintUsage(os.Stderr, environmentGitOpsUsage+" [--json]", "projects environments")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	status, err := client.GetEnvironmentGitOps(context.Background(), target[0], target[1])
	if err != nil {
		return printErr("Environment GitOps explanation failed", err)
	}
	explanation := status.Explanation
	if explanation == nil || !explanation.ReadOnly || explanation.SourceID != status.Source.ID || explanation.Generation != status.Source.Generation || explanation.IntentVersion != status.Source.IntentVersion {
		fallback := diagnostics.Assess(status, time.Now().UTC())
		explanation = &fallback
	}
	if *asJSON || jsonOutput {
		return environmentGitOpsOutput(explanation, nil)
	}
	if err := writeEnvironmentGitOpsExplanation(osStdout, *explanation); err != nil {
		return printErr("Could not write explanation", err)
	}
	return 0
}

func writeEnvironmentGitOpsExplanation(w io.Writer, explanation api.EnvironmentGitOpsExplanation) error {
	clean := func(value string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, value)
	}
	known := func(value string) string {
		if value == "" {
			return "unknown"
		}
		return clean(value)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "GitOps: %s\n%s\n\n", clean(explanation.Status), clean(explanation.Summary))
	fmt.Fprintf(&out, "Observed commit: %s (source %s)\nApproved revision: %s\nApplied revision: %s\nServing revision: %s\n\n",
		known(explanation.ObservedCommitSHA), clean(explanation.SourceFreshness), known(explanation.ApprovedRevisionID), known(explanation.AppliedRevisionID), known(explanation.ServingRevisionID))
	for _, workload := range explanation.Workloads {
		fmt.Fprintf(&out, "%s: artifact/build %s; qualification %s; activation %s; serving %s\n", clean(workload.Resource), known(workload.Preparation), known(workload.Qualification), known(workload.Activation), known(workload.Serving))
		if workload.BuildCacheStatus != "" {
			fmt.Fprintf(&out, "  Build cache: %s (fresh qualification remains required)\n", clean(workload.BuildCacheStatus))
		}
		if workload.BuildStatus != "" {
			fmt.Fprintf(&out, "  Recorded build: %s\n", clean(workload.BuildStatus))
		}
	}
	for _, issue := range explanation.Issues {
		fmt.Fprintf(&out, "\n%s [%s] %s\n  %s: %s\n", clean(issue.Resource), clean(issue.Code), clean(issue.Message), clean(issue.Actor), clean(issue.Action))
	}
	out.WriteString("\nRead-only assessment. Unknown or missing evidence does not establish readiness; execution gates remain separate.\n")
	_, err := io.WriteString(w, out.String())
	return err
}
