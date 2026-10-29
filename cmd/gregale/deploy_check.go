package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

func cmdDeployCheck(args []string) int {
	// ADR-20261026145832: deploy check is the one pre-deploy check. Without
	// --project it checks local source or an image, as `gregale doctor` does.
	if !slices.ContainsFunc(args, func(arg string) bool { return arg == "--project" || arg == "--project=true" }) {
		return cmdDoctor(args)
	}
	fs := newFlagSet("deploy check", flag.ContinueOnError)
	project := fs.Bool("project", false, "check the complete project without submitting a deployment")
	slug := fs.String("project-slug", "", "project slug (defaults to linked context or selected directory)")
	path := fs.String("path", "", "selected local source directory")
	source := fs.String("source", "auto", "local source policy: auto, head or worktree")
	worktree := fs.Bool("worktree", false, "check local working tree changes")
	request := api.ProjectDeploymentCheckRequest{}
	fs.StringVar(&request.Environment, "environment", "production", "destination environment")
	fs.StringVar(&request.FromEnvironment, "from", "", "check an environment promotion instead of local source")
	fs.StringVar(&request.ExpectedStateHash, "expected-state", "", "compare the state hash from a previous check")
	fs.BoolVar(&request.Atomic, "atomic", false, "check atomic source release prerequisites")
	fs.BoolVar(&request.AddServices, "add-services", false, "check HTTP service addition prerequisites")
	fs.BoolVar(&request.Verify, "verify", false, "check critical health verification prerequisites")
	fs.BoolVar(&request.Workers, "workers", false, "check push worker release prerequisites")
	fs.BoolVar(&request.WorkerHealth, "worker-health", false, "require worker delivery health during deployment")
	fs.BoolVar(&request.Migrate, "migrate", false, "check a declared backward-compatible migration")
	fs.BoolVar(&request.Progressive, "progressive", false, "check progressive rollout prerequisites")
	fs.BoolVar(&request.Stabilize, "stabilize", false, "check post-success monitoring prerequisites")
	fs.BoolVar(&request.SyncConfig, "sync-config", false, "check synchronized environment promotion configuration")
	configFile := fs.String("config-file", "", "proposed paired non-secret release configuration")
	secretsFile := fs.String("secrets-file", "", "proposed secret file; only key names leave this machine")
	if fs.Parse(args) != nil || fs.NArg() != 0 || !*project {
		PrintUsage(osStderr, "usage: gregale deploy check --project [--project-slug SLUG] [--path DIR] [--atomic --verify] [--from ENV --env ENV --sync-config] [--json]", "deploy")
		return 1
	}
	if request.FromEnvironment != "" && (*path != "" || *configFile != "" || *secretsFile != "" || *source != "auto" || *worktree) {
		return printErr("Invalid readiness input", errors.New("environment promotion checks cannot include local source or files"))
	}
	root, err := os.Getwd()
	if err != nil {
		return printErr("Cannot select source", err)
	}
	if *path != "" {
		root, err = filepath.Abs(*path)
		if err != nil {
			return printErr("Cannot select source", err)
		}
	}
	if *slug == "" {
		linked, _, linkErr := findProjectContext(root)
		if linkErr == nil {
			*slug = linked.Project
		} else if !errors.Is(linkErr, errProjectContextNotFound) {
			return printErr("Invalid linked project", linkErr)
		} else {
			*slug = sanitizeProjectSlug(filepath.Base(root))
		}
	}
	if !api.ValidProjectSlug(*slug) || !api.ValidProjectEnvironmentSlug(request.Environment) {
		return printErr("Invalid readiness target", errors.New("select a valid project slug and destination environment"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if *configFile != "" {
		request.ReleaseConfig, err = readProjectReleaseConfig(*configFile)
		if err != nil {
			return printErr("Invalid release configuration", err)
		}
	}
	if request.FromEnvironment == "" {
		mode, modeErr := resolveDeploySourceMode(*source, flagSetWasSet(fs)["source"], *worktree, false, false)
		if modeErr != nil {
			return printErr("Invalid source selection", modeErr)
		}
		selected, cleanup, sourceErr := deploymentCheckSource(root, *path, mode)
		if sourceErr != nil {
			return printErr("Cannot inspect selected source", sourceErr)
		}
		defer cleanup()
		if err = deploymentCheckWorkloads(selected, *secretsFile, &request); err != nil {
			return printErr("Cannot inspect project", err)
		}
	}
	result, err := client.CheckProjectDeployment(context.Background(), *slug, request)
	if err != nil {
		result = api.ProjectDeploymentReadiness{SchemaVersion: 1, ProjectSlug: *slug, Environment: request.Environment, FromEnvironment: request.FromEnvironment, Status: "unknown", CheckedAt: time.Now().UTC(), Checks: []api.ProjectDeploymentCheck{{Code: "readiness_unavailable", Status: "unknown", Message: "The server could not return a valid readiness report.", Remedy: "Check authentication and read access, retry unavailable reads, and use a server that supports deployment readiness."}}}
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(result)); code != 0 {
			return code
		}
	} else {
		renderDeploymentCheck(result)
	}
	return deploymentCheckExitCode(result)
}

func deploymentCheckSource(root, sourcePath string, mode deploySourceMode) (string, func(), error) {
	if mode == deploySourceWorktree {
		return root, func() {}, nil
	}
	var provenance zeroConfigProvenance
	var ok bool
	var err error
	if mode == deploySourceHEAD {
		provenance, err = resolveExplicitHeadProvenance(root)
		ok = err == nil
	} else {
		provenance, ok, err = resolveZeroConfigProvenance(root)
	}
	if !ok {
		if mode == deploySourceAuto && (errors.Is(err, ErrNotInGitRepo) || errors.Is(err, ErrNoGitRemote)) {
			return root, func() {}, nil
		}
		return "", nil, err
	}
	_, selected, cleanup, err := materializeCommittedGitSource(provenance, root, "", sourcePath, "")
	return selected, cleanup, err
}

func deploymentCheckWorkloads(root, secretsFile string, request *api.ProjectDeploymentCheckRequest) error {
	scan, err := reposcan.Scan(os.DirFS(root))
	if err != nil {
		return err
	}
	if len(scan.Workloads) == 0 {
		return errors.New("no deployable project workloads were detected")
	}
	var provided []string
	if secretsFile != "" {
		pairs, err := readSecretsFile(secretsFile)
		if err != nil {
			return err
		}
		for _, pair := range pairs {
			provided = append(provided, pair.Key)
		}
	}
	for _, workload := range scan.Workloads {
		item := api.ProjectDeploymentCheckWorkload{Workload: reposcan.PlanWorkload(workload), CommandShell: workload.CommandShell, ReleaseCommand: workload.ReleaseCommand, ReleaseCommandShell: workload.ReleaseCommandShell, ProvidedKeys: provided}
		manifest, present, err := gregalemanifest.Load(filepath.Join(root, filepath.FromSlash(workload.RootDir)))
		if err != nil {
			return errors.New("a workload manifest could not be parsed")
		}
		if present && manifest != nil {
			if err := manifest.Validate(); err != nil {
				return errors.New("a workload manifest is invalid")
			}
			if len(scan.Workloads) == 1 && manifest.Release != nil && manifest.Release.Command != "" {
				item.ReleaseCommand = []string{manifest.Release.Command}
				item.ReleaseCommandShell = true
			}
		}
		request.Workloads = append(request.Workloads, item)
	}
	for _, managed := range scan.Managed {
		request.Managed = append(request.Managed, managed.Name)
	}
	return nil
}

func deploymentCheckExitCode(report api.ProjectDeploymentReadiness) int {
	switch api.DeploymentReadinessStatus(report.Checks) {
	case "ready":
		return 0
	case "blocked":
		return 1
	default:
		return 2
	}
}
func renderDeploymentCheck(report api.ProjectDeploymentReadiness) {
	_, _ = fmt.Fprintf(osStdout, "Deployment readiness %s (%s): %s\n", report.ProjectSlug, report.Environment, strings.ToUpper(report.Status))
	for _, check := range report.Checks {
		label := check.Code
		if check.Workload != "" {
			label = check.Workload + "/" + label
		}
		_, _ = fmt.Fprintf(osStdout, "  %s [%s]: %s", strings.ToUpper(check.Status), label, check.Message)
		if check.Observed != nil && check.Limit != nil {
			_, _ = fmt.Fprintf(osStdout, " (required %d; limit %d)", *check.Observed, *check.Limit)
		}
		_, _ = fmt.Fprintln(osStdout)
		if check.Status != "passed" && check.Remedy != "" {
			_, _ = fmt.Fprintf(osStdout, "    Fix: %s\n", check.Remedy)
		}
	}
	_, _ = fmt.Fprintln(osStdout, "Checks cover declared inputs and current control-plane state; deployment still validates source and runtime health.")
}

func deploymentCheckCommand() cliSub {
	return cliSub{Name: "check", Short: "Check local source, an image or project prerequisites before deploying", Notes: []string{"Without --project, checks local source or an image, as `gregale doctor` does; with --project, checks the project's release prerequisites against current state."}, Examples: []string{"gregale deploy check", "gregale deploy check --image ghcr.io/acme/api:1.2 --strict", "gregale deploy check --project --json", "gregale deploy check --project --atomic --verify", "gregale deploy check --project --project-slug shop --from staging --env production --sync-config --verify"}, Flags: []cliFlag{
		{Name: "project", Short: "check the complete project's release prerequisites", Bool: true},
		{Name: "image", Short: "without --project: inspect this Linux/amd64 image without downloading layers", Value: "REF"},
		{Name: "registry-user", Short: "without --project: registry username; requires --registry-password-stdin", Value: "USER"},
		{Name: "registry-password-stdin", Short: "without --project: read the registry password or token from stdin", Bool: true},
		{Name: "strict", Short: "without --project: exit 1 on warnings", Bool: true},
		{Name: "project-slug", Short: "project slug (defaults to linked context or selected directory)", Value: "SLUG"},
		{Name: "path", Short: "selected source directory", Value: "DIR"},
		{Name: "source", Short: "local source policy (default auto)", Value: "MODE", ClosedSet: []string{"auto", "head", "worktree"}},
		{Name: "worktree", Short: "check local working tree changes", Bool: true},
		{Name: "environment", Short: "destination environment (default production)", Value: "ENV"},
		{Name: "from", Short: "source environment for a promotion check", Value: "ENV"},
		{Name: "expected-state", Short: "compare a previous check's state hash", Value: "SHA256"},
		{Name: "atomic", Short: "check atomic source release prerequisites", Bool: true},
		{Name: "add-services", Short: "check HTTP service addition prerequisites", Bool: true},
		{Name: "verify", Short: "check critical health verification prerequisites", Bool: true},
		{Name: "workers", Short: "check push worker release prerequisites", Bool: true},
		{Name: "worker-health", Short: "require worker delivery health during deployment", Bool: true},
		{Name: "migrate", Short: "check one backward-compatible migration", Bool: true},
		{Name: "progressive", Short: "check progressive rollout prerequisites", Bool: true},
		{Name: "stabilize", Short: "check post-success monitoring prerequisites", Bool: true},
		{Name: "sync-config", Short: "check synchronized promotion configuration", Bool: true},
		{Name: "config-file", Short: "proposed non-secret paired configuration", Value: "PATH"},
		{Name: "secrets-file", Short: "proposed secret input; only key names leave this machine", Value: "PATH"},
	}}
}
