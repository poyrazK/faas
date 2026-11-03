package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Retry is deliberately separate from resume: it requires source and creates
// new attempts, while resume only observes the original accepted IDs.
func cmdDeployRetry(ctx context.Context, args []string) int {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	fs := newFlagSet("deploy retry", flag.ContinueOnError)
	tarball := fs.String("tarball", "", "source archive to rebuild")
	sourcePath := fs.String("path", "", "source directory to rebuild (default: current directory)")
	sourceMode := fs.String("source", "auto", "local source to rebuild: auto, head, or worktree")
	worktree := fs.Bool("worktree", false, "include working tree changes (alias for --source=worktree)")
	yes := fs.Bool("yes", false, "confirm the retry plan")
	noWait := fs.Bool("no-wait", false, "return after acceptance")
	timeout := fs.Int("timeout", int(defaultDeployWaitTimeout/time.Second), "maximum wait seconds")
	flagArgs, positionals := splitArgsForFlags(args, "yes", "no-wait", "worktree")
	if err := fs.Parse(flagArgs); err != nil {
		return 1
	}
	if len(positionals) != 1 || *timeout <= 0 {
		return printErr("Invalid deploy retry arguments", errors.New("usage: gregale release attempt retry SESSION_ID [--path DIR] [--source auto|head|worktree] [--tarball SOURCE.tar.gz] [--yes] [--no-wait] [--timeout SECONDS]"))
	}
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	if _, err := resolveDeploySourceMode(*sourceMode, explicit["source"], *worktree, *tarball != "", false); err != nil {
		return printErr("Invalid source selection", err)
	}
	if err := validateDeploySourceSelection(*sourcePath, *worktree, "", *tarball, "", "", false, "", ""); err != nil {
		return printErr("Invalid source selection", err)
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	path, err := selectDeploySession(c, positionals, "")
	if err != nil {
		return printErr("Could not select deploy session", err)
	}
	lock, err := lockDeploySession(path)
	if err != nil {
		return printErr("Could not lock deploy session", err)
	}
	defer func() { _ = lock.Close() }()
	saved, err := loadSavedDeploySession(c, path)
	if err != nil {
		return printErr("Could not read deploy session", err)
	}
	p := saved.record.Project
	if p == nil || p.Refused {
		return printErr("Cannot retry this session", errors.New("retry requires a saved project deploy"))
	}
	checkCtx, cancel := context.WithTimeout(ctx, time.Duration(*timeout)*time.Second)
	defer cancel()
	if p.AttemptID != "" {
		if err := saved.discoverProjectSubmission(checkCtx, c); err != nil {
			return projectSubmissionFailure(saved, err)
		}
	}
	project, err := c.GetProject(checkCtx, p.Slug)
	if err != nil {
		return printErr("Could not inspect retry project", err)
	}
	names, targets, err := selectProjectRetryTargets(checkCtx, c, saved, project)
	if err != nil {
		return printErr("Project retry stopped", err)
	}
	if len(targets) == 0 {
		return printErr("Nothing to retry", errors.New("the session has no confirmed failed deployments"))
	}
	deployArgs := []string{"--project", "--project-slug", p.Slug, "--only", strings.Join(names, ","), "--no-triggers", "--timeout", strconv.Itoa(*timeout)}
	if *tarball != "" {
		deployArgs = append(deployArgs, "--tarball", *tarball)
	}
	if *sourcePath != "" {
		deployArgs = append(deployArgs, "--path", *sourcePath)
	}
	if explicit["source"] {
		deployArgs = append(deployArgs, "--source", *sourceMode)
	}
	if *worktree {
		deployArgs = append(deployArgs, "--worktree")
	}
	if p.Environment != "" {
		deployArgs = append(deployArgs, "--environment", p.Environment)
	}
	if *yes {
		deployArgs = append(deployArgs, "--yes")
	}
	if *noWait {
		deployArgs = append(deployArgs, "--no-wait")
	}
	return cmdDeployTarballToExisting(ctx, deployArgs, false, deployExecution{projectRetry: targets})
}

func selectProjectRetryTargets(ctx context.Context, c *Client, saved *savedDeploySession, project api.ProjectResponse) ([]string, map[string]string, error) {
	p := saved.record.Project
	if project.ID != p.ID {
		return nil, nil, errors.New("saved project was replaced")
	}
	workloads := make(map[string]string)
	for _, app := range project.Workloads {
		workloads[app.Slug] = app.WorkloadName
	}
	targets := make(map[string]string)
	var names []string
	for _, row := range p.Workloads {
		if row.NotAccepted {
			continue
		}
		dep, err := c.GetDeployment(ctx, row.DeploymentID)
		if err != nil {
			return nil, nil, fmt.Errorf("inspect %s: %w", row.Slug, err)
		}
		if dep.ID != row.DeploymentID || dep.AppID != row.AppID {
			return nil, nil, errors.New("saved deployment identity changed")
		}
		if dep.Status != "failed" {
			continue
		}
		name := workloads[row.Slug]
		if name == "" {
			return nil, nil, errors.New("failed workload no longer belongs to the project")
		}
		targets[row.AppID] = dep.ID
		names = append(names, name)
	}
	sort.Strings(names)
	return names, targets, nil
}

func validateProjectRetryAcceptance(targets map[string]string, apply api.ApplyResponse) error {
	seen := make(map[string]bool)
	for _, row := range apply.Builds {
		previous, expected := targets[row.AppID]
		if !expected || seen[row.AppID] || row.DeploymentID == previous {
			return errors.New("server returned unexpected retry workloads or reused the failed deployment")
		}
		seen[row.AppID] = true
	}
	if len(seen) != len(targets) {
		return errors.New("server did not retry every requested workload; upgrade APID before using project retry")
	}
	return nil
}
