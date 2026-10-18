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
	"github.com/onebox-faas/faas/pkg/environmentgitops/rollback"
)

func cmdProjectsEnvironmentGitOpsReleases(args []string) int {
	flags, target := splitArgsForFlags(args, "json")
	fs := newFlagSet("environment-gitops-releases", flag.ContinueOnError)
	commit := fs.String("target-commit", "", "immutable commit containing the current target manifest")
	before := fs.String("before", "", "next_before release UUID from the previous page")
	asJSON := fs.Bool("json", false, "emit the recovery catalog as JSON")
	request := api.ListEnvironmentGitReleasesRequest{}
	if fs.Parse(flags) != nil || !validEnvironmentGitOpsTarget(target) {
		PrintUsage(os.Stderr, environmentGitOpsUsage+" --target-commit SHA [--before UUID] [--json]", "projects environments")
		return 1
	}
	request.TargetCommitSHA, request.Before = *commit, *before
	if request.Validate() != nil {
		return printErr("Invalid recovery listing", fmt.Errorf("select an immutable target commit and canonical release cursor"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.ListEnvironmentGitReleases(context.Background(), target[0], target[1], request)
	if err != nil {
		return printErr("GitOps recovery listing failed", err)
	}
	if rollback.ValidateCatalog(out) != nil || out.Project != target[0] || out.Environment != target[1] || out.TargetCommitSHA != *commit {
		return printErr("GitOps recovery listing failed", fmt.Errorf("unverified recovery response"))
	}
	if *asJSON || jsonOutput {
		return environmentGitOpsOutput(out, nil)
	}
	if err := writeGitOpsRecoveryCatalog(osStdout, out); err != nil {
		return printErr("Could not write recovery listing", err)
	}
	return 0
}

func gitOpsRollbackCommand(project, environment, release, commit string) string {
	return fmt.Sprintf("gregale projects environments gitops rollback-plan %s %s --to-release %s --target-commit %s", project, environment, release, commit)
}

func writeGitOpsRecoveryCatalog(w io.Writer, out api.ListEnvironmentGitReleasesResponse) error {
	clean := func(value string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, value)
	}
	var text strings.Builder
	fmt.Fprintf(&text, "GitOps recovery: %s / %s\nTarget commit: %s\nObserved: %s\n\n", out.Project, out.Environment, out.TargetCommitSHA, out.ObservedAt.Format(time.RFC3339))

	if p := out.RecoveryRetention; p != nil {
		fmt.Fprintf(&text, "Artifact retention policy: up to %d releases, %d seconds, %d bytes reserved; %d bytes charged across all pages.\n\n", p.Releases, p.Seconds, p.MaxBytes, out.RetentionBytes)
	}
	for _, release := range out.Releases {
		served, expires := "unknown", "unknown"
		if release.ServedAt != nil {
			served = release.ServedAt.Format(time.RFC3339)
		}
		if release.ExpiresAt != nil {
			expires = release.ExpiresAt.Format(time.RFC3339)
		}
		fmt.Fprintf(&text, "%s: %s · manifest commit %s · served %s\n  Release window: %s (deadline %s)\n", release.ReleaseSetID, release.Eligibility, clean(release.CommitSHA), served, release.RetentionStatus, expires)

		fmt.Fprintf(&text, "  Artifact protection: %s (%d charged bytes)", clean(release.ProtectionStatus), release.RetainedBytes)
		if release.ArtifactsProtectedUntil != nil {
			fmt.Fprintf(&text, "; deadline %s", release.ArtifactsProtectedUntil.UTC().Format(time.RFC3339))
		}
		text.WriteString("\n")
		for _, artifact := range release.Artifacts {
			fmt.Fprintf(&text, "  %s: image %s · layer %s · build %s · code commit %s\n", clean(artifact.Resource), clean(artifact.ImageDigest), clean(artifact.ArtifactDigest), clean(artifact.BuildID), clean(artifact.BuildCommitSHA))
		}
		for _, reason := range release.BlockingReasons {
			fmt.Fprintf(&text, "  Blocked: %s\n", clean(reason))
		}
		fmt.Fprintf(&text, "  %s\n\n", gitOpsRollbackCommand(out.Project, out.Environment, release.ReleaseSetID, out.TargetCommitSHA))
	}
	if len(out.Releases) == 0 {
		text.WriteString("No retained serving releases for this Git binding.\n")
	}
	if out.NextBefore != "" {
		fmt.Fprintf(&text, "Next page: --before %s\n", out.NextBefore)
	}
	text.WriteString("\nEligibility is observed now. Release windows do not guarantee artifact retention. Fresh Git review and qualification remain required.\n")
	_, err := io.WriteString(w, text.String())
	return err
}
