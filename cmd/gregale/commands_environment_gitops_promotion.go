package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops/promotion"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

func cmdProjectsEnvironmentGitOpsPromotionPlan(args []string) int {
	flags, target := splitArgsForFlags(args, "fail-on-blocked")
	fs := newFlagSet("environment-gitops-promotion-plan", flag.ContinueOnError)
	from := fs.String("from", "", "source environment with a proven serving release")
	commit := fs.String("target-commit", "", "immutable commit containing the target manifest")
	format := fs.String("format", "json", "json, markdown, patch or manifest")
	expected := fs.String("expected-plan-hash", "", "recheck a reviewed observation before exporting")
	manifest := fs.String("manifest", "", "local target YAML/JSON file for manifest export")
	fail := fs.Bool("fail-on-blocked", false, "exit 2 after printing a blocked plan")
	if fs.Parse(flags) != nil || !validEnvironmentGitOpsTarget(target) || !api.ValidProjectEnvironmentSlug(*from) || *from == target[1] || !validGitOpsPlanCommit(*commit) ||
		(*format != "json" && *format != "markdown" && *format != "patch" && *format != "manifest") ||
		((*format == "patch" || *format == "manifest") && len(*expected) != 64) || (*format == "manifest") != (*manifest != "") || jsonOutput && *format != "json" {
		PrintUsage(os.Stderr, environmentGitOpsUsage+" --from ENV --target-commit SHA [--format json|markdown|patch|manifest]", "projects environments")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	request := api.PreviewEnvironmentGitPromotionRequest{FromEnvironment: *from, TargetCommitSHA: *commit, ExpectedPlanHash: *expected}
	out, err := client.PreviewEnvironmentGitPromotion(context.Background(), target[0], target[1], request)
	if err != nil {
		return printErr("GitOps promotion plan failed", err)
	}
	if err := promotion.Validate(out); err != nil || out.Project != target[0] || out.FromEnvironment != *from || out.ToEnvironment != target[1] || out.Target.CommitSHA != *commit || *expected != "" && out.PlanHash != *expected {
		return printErr("GitOps promotion plan failed", fmt.Errorf("unverified, changed or unredacted promotion response"))
	}
	if err := writeGitOpsPromotionPlan(out, *format, *manifest); err != nil {
		return printErr("Could not export promotion plan", err)
	}
	if *fail && len(out.BlockingReasons) != 0 {
		return 2
	}
	return 0
}

func writeGitOpsPromotionPlan(out api.PreviewEnvironmentGitPromotionResponse, format, manifest string) error {
	switch format {
	case "patch":
		patch, err := promotion.ExportPatch(out)
		if err != nil {
			return err
		}
		if environmentGitOpsOutput(patch, nil) != 0 {
			return fmt.Errorf("could not write patch")
		}
	case "manifest":
		file, err := openCustomerFile(manifest)
		if err != nil {
			return err
		}
		raw, err := io.ReadAll(io.LimitReader(file, int64(api.EnvironmentGitOpsMaxDefinitionBytes)+1))
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		definition, err := gregalemanifest.ParseEnvironment(raw)
		if err != nil {
			return err
		}
		encoded, err := promotion.ExportManifest(out, definition)
		if err != nil {
			return err
		}
		_, err = osStdout.Write(encoded)
		return err
	case "markdown":
		_, err := fmt.Fprintf(osStdout, "### GitOps promotion: %s / %s → %s\n\nPlan `%s` · source release `%s` · target commit `%s`\n\nRead-only proposal. Target review and fresh qualification remain required.\n\n",
			gitOpsMarkdownText(out.Project), gitOpsMarkdownText(out.FromEnvironment), gitOpsMarkdownText(out.ToEnvironment), out.PlanHash, gitOpsMarkdownText(out.Origin.ReleaseSetID), out.Target.CommitSHA)
		if err != nil {
			return err
		}
		for _, change := range out.Changes {
			if _, err := fmt.Fprintf(osStdout, "- %s: `%s` → `%s`\n", gitOpsMarkdownText(change.Resource), gitOpsMarkdownText(gitOpsPromotionSourceLabel(change.Before)), gitOpsMarkdownText(gitOpsPromotionSourceLabel(change.After))); err != nil {
				return err
			}
		}
		for _, artifact := range out.Artifacts {
			if artifact.BuildArtifactID == "" {
				continue
			}
			if _, err := fmt.Fprintf(osStdout, "- %s: layer `%s` · build `%s` · code commit `%s`\n", gitOpsMarkdownText(artifact.Resource), gitOpsMarkdownText(artifact.ArtifactDigest), gitOpsMarkdownText(artifact.BuildID), gitOpsMarkdownText(artifact.BuildCommitSHA)); err != nil {
				return err
			}
		}
		for _, reason := range out.BlockingReasons {
			if _, err := fmt.Fprintf(osStdout, "- Blocked: %s\n", gitOpsMarkdownText(reason)); err != nil {
				return err
			}
		}
	default:
		if environmentGitOpsOutput(out, nil) != 0 {
			return fmt.Errorf("could not write promotion plan")
		}
	}
	return nil
}

func gitOpsPromotionSourceLabel(source api.EnvironmentWorkloadSource) string {
	if source.Kind == "image" {
		return source.Image
	}
	if source.BuildArtifactID != "" {
		return source.Kind + " artifact " + source.BuildArtifactID + " input sha256:" + source.BuildArtifactHash
	}
	return source.Kind + " " + source.Directory
}
