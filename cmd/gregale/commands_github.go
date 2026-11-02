package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdGithub exposes the bearer-key GitHub connection contract. The existing
// `connect github` command remains the browser OAuth entry point; these verbs
// are for repeatable customer automation after an installation exists.
func cmdGithub(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale github <status|sync|repos|bind|setup|disconnect> <slug> [flags]", "github")
		return 1
	}
	switch args[0] {
	case "status":
		return cmdGithubStatus(args[1:])
	case "sync":
		return cmdGithubSync(args[1:])
	case "repos":
		return cmdGithubRepos(args[1:])
	case "bind":
		return cmdGithubBind(args[1:])
	case "setup":
		return cmdGithubSetup(args[1:])
	case "disconnect":
		return cmdGithubDisconnect(args[1:])
	default:
		printCommandValidation(os.Stderr, "unknown github subcommand %q\n", args[0])
		return unknownSubcommandChoices(os.Stderr, "github", nestedCliCommand("github"), args[0])
	}
}

func cmdGithubRepos(args []string) int {
	fs := newFlagSet("github repos", flag.ContinueOnError)
	installationID := fs.Int64("installation-id", 0, "GitHub App installation id (defaults to the app's, or the account's only one)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || !validCLISlug(fs.Arg(0)) || *installationID < 0 {
		PrintUsage(os.Stderr, "usage: gregale github repos <slug> [--installation-id ID]", "github")
		return 1
	}
	slug := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	// Resolve the slug first so a typo cannot accidentally list a valid
	// account-level installation while the caller meant another app.
	status, err := client.GetGitHubConnection(context.Background(), slug)
	if err != nil {
		return printErr("GitHub status failed", err)
	}
	if *installationID == 0 && status.Connected {
		*installationID = status.InstallationID
	}
	repos, err := client.ListGitHubRepositoriesForInstallation(context.Background(), *installationID)
	if err != nil {
		return printErr("GitHub repository listing failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(repos))
	}
	_, _ = fmt.Fprintf(osStdout, "GitHub repositories visible to %s:\n", slug)
	for _, repo := range repos {
		visibility := "public"
		if repo.Private {
			visibility = "private"
		}
		_, _ = fmt.Fprintf(osStdout, "  %-40s %-8s %s\n", repo.FullName, visibility, repo.DefaultBranch)
	}
	return 0
}

func cmdGithubStatus(args []string) int {
	slug, ok := githubSlugArg(args, "github status")
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	status, err := client.GetGitHubConnection(context.Background(), slug)
	if err != nil {
		return printErr("GitHub status failed", err)
	}
	return renderGitHubStatus(slug, status)
}

func cmdGithubSync(args []string) int {
	slug, ok := githubSlugArg(args, "github sync")
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	status, err := client.SyncGitHubConnection(context.Background(), slug)
	if err != nil {
		return printErr("GitHub sync failed", err)
	}
	return renderGitHubStatus(slug, status)
}

func cmdGithubBind(args []string) int {
	fs := newFlagSet("github bind", flag.ContinueOnError)
	installationID := fs.Int64("installation-id", 0, "GitHub App installation id (optional; resolved from the account when omitted)")
	repo := fs.String("repo", "", "GitHub repository OWNER/NAME (required)")
	branch := fs.String("branch", "", "production branch (defaults to the repository's default branch)")
	deployBranches := fs.String("deploy-branches", "", "comma-separated branch=scope mappings")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || !validCLISlug(fs.Arg(0)) || *installationID < 0 || strings.TrimSpace(*repo) == "" {
		PrintUsage(os.Stderr, "usage: gregale github link <slug> --repo OWNER/NAME [--installation-id ID] [--branch BRANCH] [--deploy-branches branch=scope,...]", "github")
		return 1
	}
	branches, err := parseGitHubDeployBranches(*deployBranches)
	if err != nil {
		return printErr("Invalid --deploy-branches", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if *installationID == 0 {
		status, err := client.GetGitHubConnection(context.Background(), fs.Arg(0))
		if err != nil {
			return printErr("GitHub status failed", err)
		}
		resolved, err := githubBindInstallation(status)
		if err != nil {
			return printErr("GitHub bind failed", err)
		}
		*installationID = resolved
	}
	resp, err := client.BindGitHubConnection(context.Background(), fs.Arg(0), api.InstallBindRequest{
		InstallationID: *installationID, RepoFullName: strings.TrimSpace(*repo),
		ProductionBranch: strings.TrimSpace(*branch), DeployBranches: branches,
	})
	if err != nil {
		return printErr("GitHub bind failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "GitHub repository %s bound to %s.", resp.RepoFullName, fs.Arg(0))
	_, _ = fmt.Fprintf(osStdout, "  binding_id:        %s\n  production_branch: %s\n", resp.BindingID, resp.ProductionBranch)
	return 0
}

func cmdGithubDisconnect(args []string) int {
	fs := newFlagSet("github disconnect", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "confirm removing the repository binding")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || !validCLISlug(fs.Arg(0)) || !*yes {
		PrintUsage(os.Stderr, "usage: gregale github disconnect <slug> --yes", "github")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	status, err := client.DisconnectGitHubConnection(context.Background(), fs.Arg(0))
	if err != nil {
		return printErr("GitHub disconnect failed", err)
	}
	return renderGitHubStatus(fs.Arg(0), status)
}

// githubBindInstallation picks the installation a bind uses when none was
// named: the app's current one, else the account's only one.
func githubBindInstallation(status api.GitHubInstallStatus) (int64, error) {
	if status.InstallationID > 0 {
		return status.InstallationID, nil
	}
	if len(status.Installations) > 1 {
		ids := make([]string, 0, len(status.Installations))
		for _, install := range status.Installations {
			label := strconv.FormatInt(install.InstallationID, 10)
			if install.GitHubLogin != "" {
				label += " (" + install.GitHubLogin + ")"
			}
			ids = append(ids, label)
		}
		return 0, fmt.Errorf("this account has several GitHub installations (%s); pass --installation-id", strings.Join(ids, ", "))
	}
	return 0, fmt.Errorf("no GitHub App installation is connected; run `gregale github connect` first")
}

func githubSlugArg(args []string, command string) (string, bool) {
	if len(args) != 1 || !validCLISlug(args[0]) {
		PrintUsage(os.Stderr, "usage: gregale "+command+" <slug>", "github")
		return "", false
	}
	return args[0], true
}

func renderGitHubStatus(slug string, status api.GitHubInstallStatus) int {
	if jsonOutput {
		return jsonOut(writeJSON(status))
	}
	_, _ = fmt.Fprintf(osStdout, "GitHub connection for %s: %s (%s)\n", slug, status.State, status.Health)
	if status.InstallationID > 0 {
		_, _ = fmt.Fprintf(osStdout, "  installation_id: %d\n", status.InstallationID)
	}
	if !status.Connected && len(status.Installations) > 1 {
		_, _ = fmt.Fprintln(osStdout, "  installations:")
		for _, install := range status.Installations {
			_, _ = fmt.Fprintf(osStdout, "    %d %s\n", install.InstallationID, install.GitHubLogin)
		}
	}
	if status.RepoFullName != "" {
		_, _ = fmt.Fprintf(osStdout, "  repository:       %s\n  production_branch: %s\n", status.RepoFullName, status.ProductionBranch)
	}
	if status.LastReconcileError != "" {
		_, _ = fmt.Fprintf(osStdout, "  last_reconcile_error: %s\n", status.LastReconcileError)
	}
	if status.SyncResult != nil {
		_, _ = fmt.Fprintf(osStdout, "  synced: detached=%t repositories=%d at=%s\n", status.SyncResult.Detached, status.SyncResult.RemoteRepositoryCount, status.SyncResult.SyncedAt.Format("2006-01-02T15:04:05Z07:00"))
	}
	return 0
}

func parseGitHubDeployBranches(raw string) (map[string]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	out := make(map[string]string)
	for _, item := range strings.Split(raw, ",") {
		parts := strings.SplitN(strings.TrimSpace(item), "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || api.ValidateScope(strings.TrimSpace(parts[1])) != nil {
			return nil, fmt.Errorf("expected branch=scope pairs with valid scopes")
		}
		out[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	return out, nil
}
