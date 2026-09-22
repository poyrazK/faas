package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/onebox-faas/faas/pkg/api"
)

const defaultGithubSetupWorkflow = ".github/workflows/gregale.yml"

const (
	githubSetupRolloutStandard = "standard"
	githubSetupRolloutSafe     = "safe"
)

type githubSetupReceipt struct {
	App              string                           `json:"app"`
	Repo             string                           `json:"repo"`
	ProductionBranch string                           `json:"production_branch"`
	WorkflowPath     string                           `json:"workflow_path"`
	Rollout          string                           `json:"rollout"`
	WorkflowChanged  bool                             `json:"workflow_changed"`
	WorkflowWritten  bool                             `json:"workflow_written"`
	DryRun           bool                             `json:"dry_run,omitempty"`
	Binding          *api.InstallBindResponse         `json:"binding,omitempty"`
	Policy           *api.GitHubDeploymentPolicy      `json:"policy,omitempty"`
	PolicyPatch      *api.GitHubDeploymentPolicyPatch `json:"policy_patch,omitempty"`
	WorkflowContent  string                           `json:"workflow_content,omitempty"`
}

// cmdGithubSetup turns the existing GitHub bind, preview policy, and Actions
// primitives into one safe bootstrap flow. Local file conflicts are checked
// before any API mutation; a dry run performs no auth, network, or filesystem
// writes.
func cmdGithubSetup(args []string) int {
	fs := newFlagSet("github setup", flag.ContinueOnError)
	repo := fs.String("repo", "", "GitHub repository OWNER/NAME (required for a dry run; otherwise defaults to the current binding)")
	productionBranch := fs.String("production-branch", "", "production branch (defaults to the current binding or main)")
	deployBranches := fs.String("deploy-branches", "", "comma-separated branch=scope mappings")
	workflow := fs.String("workflow", defaultGithubSetupWorkflow, "workflow path relative to the repository root")
	preview := fs.Bool("preview", false, "enable pull-request previews")
	noPreview := fs.Bool("no-preview", false, "disable pull-request previews")
	previewTTLHours := fs.Int("preview-ttl-hours", 0, "preview lease in hours (1-720)")
	rootDir := fs.String("root-dir", "", "repository-relative source root for the root workload")
	ignore := fs.String("ignore", "", "comma-separated ignored change paths")
	rollout := fs.String("rollout", githubSetupRolloutStandard, "production rollout mode: standard|safe (safe requires Pro/Scale)")
	dryRun := fs.Bool("dry-run", false, "show the workflow without writing or changing remote state")
	force := fs.Bool("force", false, "overwrite an existing workflow file")

	flags, positional := splitArgsForFlags(args, "preview", "no-preview", "dry-run", "force")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || !validCLISlug(positional[0]) {
		PrintUsage(os.Stderr, "usage: gregale github setup <slug> [flags]", "github")
		return 1
	}
	if *preview && *noPreview {
		return printErr("Invalid preview flags", errors.New("--preview and --no-preview cannot be used together"))
	}
	if *previewTTLHours != 0 && (*previewTTLHours < 1 || *previewTTLHours > 720) {
		return printErr("Invalid --preview-ttl-hours", errors.New("must be between 1 and 720 hours"))
	}
	if !validGithubSetupRollout(*rollout) {
		return printErr("Invalid --rollout", errors.New("must be standard or safe"))
	}

	workflowPath, err := githubSetupWorkflowPath(*workflow)
	if err != nil {
		return printErr("Invalid --workflow", err)
	}
	parsedBranches, err := parseGithubSetupDeployBranches(*deployBranches)
	if err != nil {
		return printErr("Invalid --deploy-branches", err)
	}
	ignoredPaths, err := parseGithubSetupIgnoredPaths(*ignore)
	if err != nil {
		return printErr("Invalid --ignore", err)
	}
	if *rootDir != "" {
		if _, err := githubSetupRepoPath(*rootDir, "--root-dir"); err != nil {
			return printErr("Invalid --root-dir", err)
		}
	}

	repoName := strings.TrimSpace(*repo)
	if repoName != "" && !validGithubSetupRepo(repoName) {
		return printErr("Invalid --repo", errors.New("expected a GitHub repository in OWNER/NAME form"))
	}
	branch := strings.TrimSpace(*productionBranch)
	if branch != "" && !validGithubSetupBranch(branch) {
		return printErr("Invalid --production-branch", errors.New("contains an unsupported Git branch character"))
	}

	root, err := gitRootFromCwd(".")
	if err != nil {
		if errors.Is(err, ErrNotInGitRepo) {
			return printErr("GitHub setup requires a Git repository", errors.New("run this command from inside the repository that should receive the workflow"))
		}
		return printErr("Could not locate the Git repository", err)
	}
	workflowFile := filepath.Join(root, workflowPath)
	policyPatch := githubSetupPolicyPatch(*preview, *noPreview, *previewTTLHours, *rootDir, ignoredPaths)
	if *dryRun {
		if repoName == "" {
			return printErr("Dry run needs --repo", errors.New("pass --repo OWNER/NAME so the workflow can be rendered without reading remote state"))
		}
		if branch == "" {
			branch = "main"
		}
		desired := renderGithubSetupWorkflow(positional[0], repoName, branch, *rollout)
		existing, exists, err := readGithubSetupWorkflow(workflowFile)
		if err != nil {
			return printErr("Could not inspect the workflow file", err)
		}
		changed := !exists || !bytes.Equal(existing, []byte(desired))
		if exists && changed && !*force {
			return printErr("Workflow already exists", fmt.Errorf("%s differs from the generated workflow; review it or rerun with --force", workflowPath))
		}
		receipt := githubSetupReceipt{
			App:              positional[0],
			Repo:             repoName,
			ProductionBranch: branch,
			WorkflowPath:     workflowPath,
			Rollout:          *rollout,
			WorkflowChanged:  changed,
			DryRun:           true,
			PolicyPatch:      policyPatch,
			WorkflowContent:  desired,
		}
		return renderGithubSetupReceipt(receipt, existing, exists)
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	status, err := client.GetGitHubConnection(ctx, positional[0])
	if err != nil {
		return printErr("GitHub status failed", err)
	}

	if repoName == "" {
		repoName = strings.TrimSpace(status.RepoFullName)
		if repoName == "" {
			return printErr("GitHub setup needs a repository", errors.New("pass --repo OWNER/NAME or bind the app before running setup"))
		}
		if !validGithubSetupRepo(repoName) {
			return printErr("GitHub setup found an invalid repository binding", errors.New("rerun with --repo OWNER/NAME"))
		}
	}
	if branch == "" {
		branch = strings.TrimSpace(status.ProductionBranch)
		if branch == "" {
			branch = "main"
		}
	}
	if !validGithubSetupBranch(branch) {
		return printErr("Invalid production branch", errors.New("the bound or requested branch contains an unsupported character"))
	}
	desired := renderGithubSetupWorkflow(positional[0], repoName, branch, *rollout)
	existing, exists, err := readGithubSetupWorkflow(workflowFile)
	if err != nil {
		return printErr("Could not inspect the workflow file", err)
	}
	changed := !exists || !bytes.Equal(existing, []byte(desired))
	if exists && changed && !*force {
		return printErr("Workflow already exists", fmt.Errorf("%s differs from the generated workflow; review it or rerun with --force", workflowPath))
	}
	receipt := githubSetupReceipt{
		App:              positional[0],
		Repo:             repoName,
		ProductionBranch: branch,
		WorkflowPath:     workflowPath,
		Rollout:          *rollout,
		WorkflowChanged:  changed,
		PolicyPatch:      policyPatch,
	}

	bindingMutation := strings.TrimSpace(*repo) != "" || strings.TrimSpace(*productionBranch) != "" || strings.TrimSpace(*deployBranches) != ""
	if bindingMutation {
		if status.InstallationID <= 0 {
			return printErr("GitHub bind failed", errors.New("no GitHub App installation is connected; run `gregale connect github` first"))
		}
		bound, err := client.BindGitHubConnection(ctx, positional[0], api.InstallBindRequest{
			InstallationID:   status.InstallationID,
			RepoFullName:     repoName,
			ProductionBranch: branch,
			DeployBranches:   parsedBranches,
		})
		if err != nil {
			return printErr("GitHub bind failed", err)
		}
		receipt.Binding = &bound
	}
	if policyPatch != nil {
		policy, err := client.PatchGitHubDeploymentPolicy(ctx, positional[0], *policyPatch)
		if err != nil {
			return printErr("GitHub deployment policy update failed", err)
		}
		receipt.Policy = &policy
	}
	if changed {
		if err := writeGithubSetupWorkflow(workflowFile, []byte(desired)); err != nil {
			return printErr("Could not write the workflow file", err)
		}
		receipt.WorkflowWritten = true
	}

	return renderGithubSetupReceipt(receipt, existing, exists)
}

func parseGithubSetupDeployBranches(raw string) (map[string]string, error) {
	branches, err := parseGitHubDeployBranches(raw)
	if err != nil {
		return nil, err
	}
	for branch := range branches {
		if !validGithubSetupBranch(branch) {
			return nil, fmt.Errorf("invalid branch %q", branch)
		}
	}
	return branches, nil
}

func parseGithubSetupIgnoredPaths(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 64 {
		return nil, errors.New("at most 64 ignored paths are supported")
	}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		path := strings.TrimSpace(part)
		if path == "" {
			return nil, errors.New("paths must not be empty")
		}
		if len(path) > 255 || strings.IndexFunc(path, func(r rune) bool { return unicode.IsControl(r) }) >= 0 {
			return nil, fmt.Errorf("invalid ignored path %q", path)
		}
		out = append(out, path)
	}
	return out, nil
}

func githubSetupPolicyPatch(preview, noPreview bool, ttl int, rootDir string, ignored []string) *api.GitHubDeploymentPolicyPatch {
	if !preview && !noPreview && ttl == 0 && rootDir == "" && ignored == nil {
		return nil
	}
	patch := &api.GitHubDeploymentPolicyPatch{}
	if preview || noPreview {
		value := preview
		patch.PreviewEnabled = &value
	}
	if ttl != 0 {
		patch.PreviewTTLHours = &ttl
	}
	if rootDir != "" {
		patch.RootDir = &rootDir
	}
	if ignored != nil {
		patch.IgnoredPaths = &ignored
	}
	return patch
}

func githubSetupWorkflowPath(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("path must not be empty")
	}
	if filepath.IsAbs(raw) {
		return "", errors.New("path must be relative to the repository root")
	}
	clean := filepath.Clean(raw)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("path must stay inside the repository")
	}
	return clean, nil
}

func githubSetupRepoPath(raw, flagName string) (string, error) {
	if filepath.IsAbs(raw) {
		return "", errors.New("must be relative to the repository root")
	}
	clean := filepath.Clean(raw)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s must stay inside the repository", flagName)
	}
	return clean, nil
}

func validGithubSetupRepo(repo string) bool {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	for _, part := range parts {
		if strings.IndexFunc(part, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
			return false
		}
		if part == "." || part == ".." {
			return false
		}
	}
	return true
}

func validGithubSetupBranch(branch string) bool {
	if branch == "" || len(branch) > 255 || strings.HasPrefix(branch, "/") || strings.HasSuffix(branch, "/") || strings.Contains(branch, "..") {
		return false
	}
	// ContainsAny takes literal characters, not a regular-expression range.
	// Check controls explicitly so hyphenated branches remain valid.
	for _, r := range branch {
		if r <= 0x20 || r == 0x7f {
			return false
		}
	}
	return !strings.ContainsAny(branch, "~^:?*[\\")
}

func validGithubSetupRollout(rollout string) bool {
	return rollout == githubSetupRolloutStandard || rollout == githubSetupRolloutSafe
}

func renderGithubSetupWorkflow(app, repo, branch, rollout string) string {
	if branch == "" {
		branch = "main"
	}
	if rollout == "" {
		rollout = githubSetupRolloutStandard
	}
	quotedBranch := strconv.Quote(branch)
	rolloutInput := ""
	if rollout == githubSetupRolloutSafe {
		rolloutInput = "          # Balanced health-gated rollout; available on Pro/Scale.\n          rollout: \"safe\"\n"
	}
	return fmt.Sprintf(`# Gregale deploy · generated by `+"`gregale github setup`"+`
# App: %s · Repo: %s · Production branch: %s
# PR previews are managed by the connected GitHub integration.
name: Gregale deploy
on:
  push:
    branches:
      - %s
  workflow_dispatch:

jobs:
  deploy:
    runs-on: ubuntu-22.04
    environment: production
    permissions:
      contents: read
      checks: write
      id-token: write
    steps:
      - uses: %s/%s@%s
        with:
          api-base: %s
          app: %s
          repo: ${{ github.repository }}
          ref: ${{ github.sha }}
          wait: "true"
          wait-timeout: "1200"
%s`, app, repo, branch, quotedBranch, githubActionRepo, githubActionPath, githubActionVersion, defaultAPIBase, app, rolloutInput)
}

func readGithubSetupWorkflow(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func writeGithubSetupWorkflow(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create workflow directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".gregale-workflow-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary workflow: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if err := tmp.Chmod(0o644); err != nil {
		cleanup()
		return fmt.Errorf("set workflow permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write temporary workflow: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync workflow: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temporary workflow: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("commit workflow: %w", err)
	}
	return nil
}

func renderGithubSetupReceipt(receipt githubSetupReceipt, existing []byte, exists bool) int {
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	status := "completed"
	if receipt.DryRun {
		status = "preview"
	}
	_, _ = fmt.Fprintf(osStdout, "GitHub setup %s for %s.\n", status, receipt.App)
	_, _ = fmt.Fprintf(osStdout, "  repository:        %s\n  production_branch: %s\n  rollout:            %s\n  workflow:          %s\n", receipt.Repo, receipt.ProductionBranch, receipt.Rollout, receipt.WorkflowPath)
	switch {
	case receipt.DryRun && receipt.WorkflowChanged:
		_, _ = fmt.Fprintln(osStdout, "  file:              would be written")
	case receipt.DryRun:
		_, _ = fmt.Fprintln(osStdout, "  file:              already matches")
	case receipt.WorkflowWritten:
		_, _ = fmt.Fprintln(osStdout, "  file:              written")
	case exists:
		_, _ = fmt.Fprintln(osStdout, "  file:              already matches")
	default:
		_, _ = fmt.Fprintln(osStdout, "  file:              unchanged")
	}
	if receipt.Binding != nil {
		_, _ = fmt.Fprintf(osStdout, "  binding:           %s\n", receipt.Binding.RepoFullName)
	}
	if receipt.Policy != nil {
		_, _ = fmt.Fprintf(osStdout, "  previews:          %t (%dh TTL)\n", receipt.Policy.PreviewEnabled, receipt.Policy.PreviewTTLHours)
	}
	if receipt.DryRun {
		_, _ = fmt.Fprintln(osStdout)
		_, _ = fmt.Fprintln(osStdout, "Generated workflow:")
		_, _ = fmt.Fprintln(osStdout)
		_, _ = fmt.Fprint(osStdout, receipt.WorkflowContent)
	}
	return 0
}
