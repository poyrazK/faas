package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/onebox-faas/faas/pkg/dataapi/integration"
)

func cmdDataAPIIntegrate(args []string) int {
	for _, arg := range args {
		if arg == "--hosted" || arg == "-hosted" || strings.HasPrefix(arg, "--hosted=") || strings.HasPrefix(arg, "-hosted=") {
			return cmdDataAPIIntegrateHosted(args)
		}
	}
	fs := newFlagSet("data-api integrate", flag.ContinueOnError)
	directory := fs.String("directory", ".", "existing TypeScript project directory")
	client := fs.String("client-directory", ".", "npm client directory relative to the project")
	bundle := fs.String("bundle", "", "approved local data-api-bundle.json")
	version := fs.String("sdk-version", "", "exact public @gregale/data version (alternative to --bundle)")
	numericMode := fs.String("numeric-mode", "number", "numeric decoding for the generated client and types (number or exact)")
	dryRun := fs.Bool("dry-run", false, "preview files and installation without writing or starting services")
	resume := fs.Bool("resume", false, "continue an existing integration without rewriting its setup")
	if parseInterspersed(fs, args) != nil {
		return 1
	}
	if fs.NArg() != 0 || *directory == "" || (*bundle == "") == (*version == "") || (*version != "" && !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`).MatchString(*version)) {
		PrintUsage(os.Stderr, "usage: gregale data-api integrate [--directory DIR] [--client-directory DIR] (--bundle MANIFEST | --sdk-version VERSION) [--numeric-mode number|exact] [--resume] [--dry-run]", "data-api")
		return 1
	}
	build := integration.BuildWithNumericMode
	if *resume {
		build = integration.Resume
	}
	plan, err := build(*directory, *client, *numericMode)
	if err != nil {
		return printErr("Could not plan Data API integration", err)
	}
	manifest := ""
	if *bundle != "" {
		manifest, err = filepath.Abs(*bundle)
		if err != nil {
			return printErr("Invalid bundle", err)
		}
		info, err := os.Lstat(manifest)
		if err != nil || !info.Mode().IsRegular() {
			return printErr("Invalid bundle", errors.New("use an existing regular bundle manifest"))
		}
	}
	steps := dataAPIIntegrationSteps(plan.Directory, plan.ClientDirectory, manifest, *version)
	if plan.WorkspaceRoot != "" {
		workspace, err := filepath.Rel(filepath.Join(plan.Directory, plan.WorkspaceRoot), filepath.Join(plan.Directory, plan.ClientDirectory))
		if err != nil || !filepath.IsLocal(workspace) {
			return printErr("Invalid workspace", errors.New("client must be inside its workspace root"))
		}
		if *version != "" {
			steps[1].Args = []string{"install", "--prefix", filepath.Join(plan.Directory, plan.WorkspaceRoot), "--workspace", filepath.ToSlash(workspace), "--save-exact", "--ignore-scripts", "--no-audit", "--no-fund", "@gregale/data@" + *version}
		} else {
			steps[1].Args = append(steps[1].Args, "--workspace-root", plan.WorkspaceRoot)
		}
	}
	if *resume {
		steps = steps[1:]
	}
	if jsonOutput {
		if err := writeNDJSON([]map[string]any{{"event": "integration_plan", "plan": plan, "steps": steps, "dry_run": *dryRun}}); err != nil {
			return printErr("Could not write integration plan", err)
		}
	} else {
		if *resume {
			_, _ = fmt.Fprintln(osStdout, "Data API integration will resume installation and verification:")
		} else {
			_, _ = fmt.Fprintln(osStdout, "Data API integration adds an isolated example; your existing application and scripts are retained:")
		}
		for _, file := range plan.Files {
			_, _ = fmt.Fprintf(osStdout, "  %s %s\n", file.Action, file.Path)
		}
		_, _ = fmt.Fprintf(osStdout, "  generate %s\n  install @gregale/data; update the selected client's package.json and npm lockfile\n", plan.GeneratedTypes)
		if manifest != "" {
			_, _ = fmt.Fprintln(osStdout, "  create data-api-artifacts.json and client vendor/gregale-data.tgz")
		}
	}
	if *dryRun {
		return 0
	}
	lockID := sha256.Sum256([]byte(plan.Directory))
	lock := flock.New(filepath.Join(os.TempDir(), fmt.Sprintf("gregale-data-integrate-%x.lock", lockID)))
	locked, err := lock.TryLock()
	if err != nil {
		return printErr("Could not lock integration", err)
	}
	if !locked {
		return printErr("Integration already running", errors.New("wait for the other integration to finish"))
	}
	defer func() { _ = lock.Unlock() }()
	if *resume {
		current, err := integration.Resume(plan.Directory, plan.ClientDirectory, *numericMode)
		if err != nil {
			return printErr("Could not resume Data API integration", err)
		}
		if current.WorkspaceRoot != plan.WorkspaceRoot {
			return printErr("Could not resume Data API integration", errors.New("the selected npm workspace changed after planning"))
		}
	}
	for _, tool := range []string{"node", "npm", "docker"} {
		if _, err := exec.LookPath(tool); err != nil {
			return printErr("Missing integration prerequisite", fmt.Errorf("install %s before integration", tool))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}").Run(); err != nil {
		return printErr("Local Docker is unavailable; the project is unchanged", errors.New("start the Docker daemon, then retry integration"))
	}
	if plan.WorkspaceRoot != "" {
		workspace, err := filepath.Rel(filepath.Join(plan.Directory, plan.WorkspaceRoot), filepath.Join(plan.Directory, plan.ClientDirectory))
		if err != nil {
			return printErr("Invalid workspace", err)
		}
		check := exec.CommandContext(ctx, "npm", "pkg", "get", "name", "--workspace", filepath.ToSlash(workspace), "--offline")
		check.Dir = filepath.Join(plan.Directory, plan.WorkspaceRoot)
		if err := check.Run(); err != nil {
			return printErr("Invalid npm workspace", errors.New("npm could not resolve the selected client workspace"))
		}
	} else {
		check := exec.CommandContext(ctx, "npm", "prefix", "--offline")
		check.Dir = filepath.Join(plan.Directory, plan.ClientDirectory)
		prefix, err := check.Output()
		if err != nil {
			return printErr("Invalid npm project", errors.New("npm could not resolve the selected client"))
		}
		npmRoot, err := filepath.EvalSymlinks(strings.TrimSpace(string(prefix)))
		if err != nil || npmRoot != check.Dir {
			return printErr("Invalid npm project", errors.New("select the npm workspace root as --directory and the member as --client-directory"))
		}
	}
	code := runDataAPIOnboarding(steps, func(step dataAPIOnboardingStep) int {
		if jsonOutput {
			original := osStdout
			osStdout = osStderr
			defer func() { osStdout = original }()
		}
		if step.Name == "setup" {
			if err := plan.Apply(); err != nil {
				return printErr("Could not apply integration", err)
			}
			return 0
		}
		if step.Command == "npm" {
			command := exec.Command("npm", step.Args...)
			command.Stdout, command.Stderr = os.Stderr, os.Stderr
			if err := command.Run(); err != nil {
				return printErr("SDK installation failed", err)
			}
			return 0
		}
		return cmdDataAPI(append([]string{step.Command}, step.Args...))
	}, func(name, status string, elapsed time.Duration) {
		if jsonOutput {
			_ = writeNDJSON([]map[string]any{{"event": "integration_step", "step": name, "status": status, "duration_ms": elapsed.Milliseconds()}})
		} else {
			_, _ = fmt.Fprintf(osStdout, "Data API integration %s: %s\n", name, status)
		}
	})
	if code == 0 {
		_, _ = fmt.Fprintf(os.Stderr, "Start local development: gregale data-api serve --directory %s --config %s --port 0 --check\n", dataAPIOnboardingQuote(plan.Directory), dataAPIOnboardingQuote(integration.Config))
	}
	return code
}

func dataAPIIntegrationSteps(root, client, bundle, version string) []dataAPIOnboardingStep {
	install := dataAPIOnboardingStep{"install", "install", []string{"--directory", root, "--client-directory", client, "--bundle", bundle}}
	if version != "" {
		install = dataAPIOnboardingStep{"install", "npm", []string{"install", "--prefix", filepath.Join(root, client), "--save-exact", "--ignore-scripts", "--no-audit", "--no-fund", "@gregale/data@" + version}}
	}
	return []dataAPIOnboardingStep{
		{"setup", "integrate", nil}, install,
		{"verify", "dev", []string{"--directory", root, "--config", integration.Config, "--port", "0", "--once", "--check", "--replay", integration.Requests}},
	}
}
