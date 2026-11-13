package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type dataAPIOnboardingStep struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

func dataAPIOnboardingSteps(root, bundle, template string) []dataAPIOnboardingStep {
	return []dataAPIOnboardingStep{
		{"scaffold", "init", []string{"--template", template, "--path", root}},
		{"install", "install", []string{"--directory", root, "--client-directory", "client", "--bundle", bundle}},
		{"verify", "dev", []string{"--directory", root, "--port", "0", "--once", "--check", "--replay", "data-api.requests.json"}},
	}
}

// Use existing owner workflows: onboarding never creates hosted resources or
// receives database credentials. Partial projects are retained for recovery.
func cmdDataAPIOnboard(args []string) int {
	fs := newFlagSet("data-api onboard", flag.ContinueOnError)
	template := fs.String("template", "data-api-starter", "starter template (data-api-starter or data-api-teams)")
	path := fs.String("path", "", "new starter project directory")
	bundle := fs.String("bundle", "", "approved local data-api-bundle.json")
	sdkVersion := fs.String("sdk-version", "", "exact public @gregale/data version (alternative to --bundle)")
	start := fs.Bool("start", false, "start the verified API and browser example")
	if parseInterspersed(fs, args) != nil {
		return 1
	}
	if fs.NArg() != 0 || *path == "" || (*bundle == "") == (*sdkVersion == "") || (*sdkVersion != "" && !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`).MatchString(*sdkVersion)) || (*template != "data-api-starter" && *template != "data-api-teams") {
		PrintUsage(os.Stderr, "usage: gregale data-api onboard --path DIR (--bundle MANIFEST | --sdk-version VERSION) [--start] [--template data-api-starter|data-api-teams]", "data-api")
		return 1
	}
	root, err := filepath.Abs(*path)
	if err != nil {
		return printErr("Invalid project path", err)
	}
	if err := checkDestEmpty(root); err != nil {
		return printErr("Choose a new or empty project directory", err)
	}
	manifest := ""
	if *bundle != "" {
		manifest, err = filepath.Abs(*bundle)
		if err != nil {
			return printErr("Invalid bundle path", err)
		}
		info, err := os.Lstat(manifest)
		if err != nil || !info.Mode().IsRegular() {
			return printErr("Invalid bundle", errors.New("use an existing regular bundle manifest"))
		}
	}
	for _, tool := range []string{"node", "npm", "docker"} {
		if _, err := exec.LookPath(tool); err != nil {
			return printErr("Missing onboarding prerequisite", fmt.Errorf("install %s before onboarding", tool))
		}
	}
	steps := dataAPIOnboardingSteps(root, manifest, *template)
	if *sdkVersion != "" {
		steps[1] = dataAPIOnboardingStep{"install", "npm", []string{"install", "--prefix", filepath.Join(root, "client"), "--save-exact", "--ignore-scripts", "--no-audit", "--no-fund", "@gregale/data@" + *sdkVersion}}
	}
	if *start {
		steps = append(steps, dataAPIOnboardingStep{"start", "dev", []string{"--directory", root, "--port", "0", "--browser"}})
	}
	return runDataAPIOnboarding(steps, func(step dataAPIOnboardingStep) int {
		if jsonOutput {
			original := osStdout
			osStdout = osStderr
			defer func() { osStdout = original }()
		}
		if step.Command == "init" {
			return cmdInit(step.Args)
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
			_ = writeNDJSON([]map[string]any{{"event": "onboarding_step", "step": name, "status": status, "duration_ms": elapsed.Milliseconds()}})
		} else if status == "failed" {
			PrintFail(osStdout, "Onboarding %s: failed (%s)", name, elapsed.Round(time.Millisecond))
		} else {
			PrintOK(osStdout, "Onboarding %s: %s (%s)", name, status, elapsed.Round(time.Millisecond))
		}
	})
}

func runDataAPIOnboarding(steps []dataAPIOnboardingStep, run func(dataAPIOnboardingStep) int, report func(string, string, time.Duration)) int {
	started := time.Now()
	for i, step := range steps {
		stepStarted := time.Now()
		if code := run(step); code != 0 {
			report(step.Name, "failed", time.Since(stepStarted))
			// POSIX single-quote arguments so recovery commands preserve literal paths.
			if i == 0 {
				return code
			}
			for _, remaining := range steps[i:] {
				if remaining.Command == "init" {
					continue
				}
				prefix := "gregale data-api "
				if remaining.Command == "npm" {
					prefix = ""
				}
				_, _ = fmt.Fprintf(os.Stderr, "Continue with: %s%s", prefix, remaining.Command)
				for _, arg := range remaining.Args {
					_, _ = fmt.Fprintf(os.Stderr, " %s", dataAPIOnboardingQuote(arg))
				}
				_, _ = fmt.Fprintln(os.Stderr)
			}
			return code
		}
		report(step.Name, "passed", time.Since(stepStarted))
	}
	report("local_activation", "passed", time.Since(started))
	return 0
}

func dataAPIOnboardingQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
