package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"github.com/onebox-faas/faas/pkg/dataapi/integration"
)

func cmdDataAPIIntegrate(args []string) int {
	fs := newFlagSet("data-api integrate", flag.ContinueOnError)
	directory := fs.String("directory", ".", "existing TypeScript project directory")
	client := fs.String("client-directory", ".", "npm client directory relative to the project")
	bundle := fs.String("bundle", "", "approved local data-api-bundle.json")
	version := fs.String("sdk-version", "", "exact public @gregale/data version (alternative to --bundle)")
	dryRun := fs.Bool("dry-run", false, "preview files and installation without writing or starting services")
	if parseInterspersed(fs, args) != nil {
		return 1
	}
	if fs.NArg() != 0 || *directory == "" || (*bundle == "") == (*version == "") || (*version != "" && !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`).MatchString(*version)) {
		PrintUsage(os.Stderr, "usage: gregale data-api integrate [--directory DIR] [--client-directory DIR] (--bundle MANIFEST | --sdk-version VERSION) [--dry-run]", "data-api")
		return 1
	}
	plan, err := integration.Build(*directory, *client)
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
	if jsonOutput {
		if err := writeNDJSON([]map[string]any{{"event": "integration_plan", "plan": plan, "steps": steps, "dry_run": *dryRun}}); err != nil {
			return printErr("Could not write integration plan", err)
		}
	} else {
		_, _ = fmt.Fprintln(osStdout, "Data API integration adds an isolated example; your existing application and scripts are retained:")
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
