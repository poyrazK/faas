package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// devSetupReceipt is the local, side-effect-free plan produced by
// `gregale dev setup`. It deliberately contains only paths, filenames, and
// counts: an env file is validated but its values never enter the receipt.
type devSetupReceipt struct {
	Version          int                    `json:"version"`
	Path             string                 `json:"path"`
	Project          string                 `json:"project"`
	Class            string                 `json:"class"`
	Framework        string                 `json:"framework,omitempty"`
	FrameworkVersion string                 `json:"framework_version,omitempty"`
	Runtime          string                 `json:"runtime,omitempty"`
	Handler          string                 `json:"handler,omitempty"`
	SourceMarkers    []string               `json:"source_markers,omitempty"`
	DependencyFiles  []string               `json:"dependency_files,omitempty"`
	EnvFile          string                 `json:"env_file,omitempty"`
	EnvKeyCount      int                    `json:"env_key_count,omitempty"`
	Hosting          *devSetupHosting       `json:"hosting,omitempty"`
	Authenticated    bool                   `json:"authenticated"`
	Ready            bool                   `json:"ready"`
	Next             []string               `json:"next"`
	StartCommand     []string               `json:"start_command,omitempty"`
	Warnings         []string               `json:"warnings,omitempty"`
	ConfigPresent    bool                   `json:"config_present"`
	Doctor           *devSetupDoctorSummary `json:"doctor,omitempty"`
}

type devSetupHosting struct {
	Start  string `json:"start,omitempty"`
	Port   int    `json:"port,omitempty"`
	Health string `json:"health,omitempty"`
}

// devSetupDoctorSummary keeps setup useful as a single first-run command
// without duplicating the full `gregale doctor` prose. The checks are the
// existing local checks, and are included in JSON for IDEs and CI.
type devSetupDoctorSummary struct {
	Checks   []doctorCheck `json:"checks"`
	Warnings int           `json:"warnings"`
	Errors   int           `json:"errors"`
}

const devSetupUsage = "usage: gregale dev setup [--path DIR] [--name PROJECT] [--env-file PATH] [--start] [--once] [--no-logs] [--open] [--postgres [--postgres-region REGION]]"

// cmdDevSetup prepares the first developer environment without making a
// remote mutation. --start hands the validated, exact plan to cmdDev, which
// owns authentication, provisioning, readiness, logs, and the watch loop.
func cmdDevSetup(args []string) int {
	fs := newFlagSet("dev setup", flag.ContinueOnError)
	sourcePath := fs.String("path", "", "source directory (relative to the current directory)")
	name := fs.String("name", "", "developer-session project name (default: selected source directory)")
	envFile := fs.String("env-file", "", "validate and sync KEY=VALUE entries as developer secrets")
	start := fs.Bool("start", false, "start the developer environment after preflight")
	once := fs.Bool("once", false, "start, sync once, and exit")
	noLogs := fs.Bool("no-logs", false, "do not attach the live runtime log stream when starting")
	open := fs.Bool("open", false, "open the developer environment URL after the first live sync")
	withPostgres := fs.Bool("postgres", false, "provision an isolated PostgreSQL database when starting")
	postgresRegion := fs.String("postgres-region", "", "managed PostgreSQL region (default: platform default)")
	if err := fs.Parse(args); err != nil {
		PrintUsage(osStderr, devSetupUsage, "dev")
		return 2
	}
	if fs.NArg() != 0 {
		PrintUsage(osStderr, devSetupUsage, "dev")
		return 2
	}
	if *once && !*start {
		return printErr("Invalid flags", errors.New("--once requires --start"))
	}
	if *noLogs && !*start {
		return printErr("Invalid flags", errors.New("--no-logs requires --start"))
	}
	if *open && !*start {
		return printErr("Invalid flags", errors.New("--open requires --start"))
	}
	if !*withPostgres && *postgresRegion != "" {
		return printErr("Invalid flags", errors.New("--postgres-region requires --postgres"))
	}

	cwd, err := os.Getwd()
	if err != nil {
		return printErr("Could not read current directory", err)
	}
	sourceDir, err := resolveDeploySourceDir(cwd, *sourcePath)
	if err != nil {
		return printErr("Invalid developer source", err)
	}
	envFilePath, envKeyCount, err := resolveSetupEnvFile(cwd, *envFile)
	if err != nil {
		return printErr("Invalid developer env file", err)
	}

	project := *name
	if project == "" {
		project = sanitizeSlug(filepath.Base(sourceDir))
	}
	if project != sanitizeSlug(project) || len(project) < 3 || len(project) > 40 {
		return printErr("Invalid --name", fmt.Errorf("use 3–40 lowercase letters, digits, and hyphens"))
	}

	config, err := resolveDevSetupSource(sourceDir)
	if err != nil {
		return printErr("Could not prepare developer setup", err)
	}
	receipt := buildDevSetupReceipt(cwd, sourceDir, project, config, envFilePath, envKeyCount, *start, *once, *noLogs, *open, *withPostgres, *postgresRegion)

	if !receipt.Authenticated {
		receipt.Warnings = append(receipt.Warnings, "not logged in; run `gregale login` before starting")
	}
	if receipt.Doctor != nil && receipt.Doctor.Errors > 0 {
		receipt.Ready = false
		receipt.Warnings = append(receipt.Warnings, "local source checks found errors; fix them before starting")
	}
	receipt.Next = devSetupNextSteps(receipt, *start)

	if jsonOutput {
		if *start {
			if err := writeNDJSON([]devSetupReceipt{receipt}); err != nil {
				return jsonOut(err)
			}
		} else if err := writeJSON(receipt); err != nil {
			return jsonOut(err)
		}
		if !receipt.Ready {
			return 1
		}
		if *start {
			return cmdDev(devSetupDevArgs(*sourcePath, *name, envFilePath, *once, *noLogs, *open, *withPostgres, *postgresRegion))
		}
		return 0
	}

	renderDevSetup(osStdout, osStderr, receipt)
	if !receipt.Ready {
		return 1
	}
	if !*start {
		return 0
	}
	PrintProgress(osStdout, "Starting the developer environment")
	return cmdDev(devSetupDevArgs(*sourcePath, *name, envFilePath, *once, *noLogs, *open, *withPostgres, *postgresRegion))
}

func resolveDevSetupSource(sourceDir string) (devSourceConfig, error) {
	manifest, present, err := gregalemanifest.Load(sourceDir)
	if err != nil {
		return devSourceConfig{}, err
	}
	if present {
		if err := manifest.Validate(); err != nil {
			return devSourceConfig{}, fmt.Errorf("gregale.yaml: %w", err)
		}
	}
	return resolveDevSourceConfig(sourceDir)
}

func resolveSetupEnvFile(cwd, raw string) (string, int, error) {
	if raw == "" {
		return "", 0, nil
	}
	path, err := resolveDevEnvFilePath(cwd, raw)
	if err != nil {
		return "", 0, err
	}
	pairs, _, err := readDevEnvFile(path)
	if err != nil {
		return "", 0, err
	}
	return path, len(pairs), nil
}

func buildDevSetupReceipt(cwd, sourceDir, project string, config devSourceConfig, envFile string, envKeyCount int, start, once, noLogs, open, postgres bool, postgresRegion string) devSetupReceipt {
	receipt := devSetupReceipt{
		Version:       1,
		Path:          sourceDir,
		Project:       project,
		Class:         devSetupShapeName(config.shape),
		Runtime:       config.runtime,
		Handler:       config.handler,
		EnvFile:       envFile,
		EnvKeyCount:   envKeyCount,
		Authenticated: strings.TrimSpace(loadToken()) != "",
		ConfigPresent: false,
	}
	if receipt.Class == "app" {
		receipt.Framework = string(detectFramework(sourceDir))
		receipt.FrameworkVersion = detectFrameworkVersion(sourceDir, framework(receipt.Framework))
	}
	receipt.SourceMarkers = devSetupSourceMarkers(sourceDir)
	receipt.DependencyFiles = devSetupDependencyFiles(sourceDir)
	if manifest, present, err := gregalemanifest.Load(sourceDir); err == nil && present {
		receipt.ConfigPresent = true
		if manifest.Hosting != nil {
			receipt.Hosting = &devSetupHosting{Start: manifest.Hosting.Start, Port: manifest.Hosting.Port, Health: manifest.Hosting.Health}
		}
	}
	receipt.StartCommand = devSetupDevArgsForDisplay(cwd, sourceDir, project, envFile, once, noLogs, open, postgres, postgresRegion)
	receipt.Doctor = buildDevSetupDoctorSummary(sourceDir, config.shape)
	receipt.Ready = receipt.Authenticated && receipt.Doctor.Errors == 0
	return receipt
}

func devSetupShapeName(s shape) string {
	if s == shapeFunction {
		return "function"
	}
	return "app"
}

func buildDevSetupDoctorSummary(sourceDir string, deploymentShape shape) *devSetupDoctorSummary {
	report := runDoctorChecksForShape(sourceDir, deploymentShape)
	summary := &devSetupDoctorSummary{Checks: report.Checks}
	for _, check := range report.Checks {
		switch check.Status {
		case "error":
			summary.Errors++
		case "warn":
			summary.Warnings++
		}
	}
	return summary
}

func devSetupSourceMarkers(sourceDir string) []string {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return nil
	}
	markers := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if _, ok := appMarker[name]; ok || functionHandlerFiles[name] {
			markers = append(markers, entry.Name())
		}
	}
	sort.Strings(markers)
	return markers
}

func devSetupDependencyFiles(sourceDir string) []string {
	candidates := []string{
		"package.json", "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "bun.lock", "bun.lockb",
		"requirements.txt", "pyproject.toml", "Pipfile", "Pipfile.lock", "poetry.lock", "uv.lock",
		"go.mod", "go.sum", "Dockerfile",
	}
	files := make([]string, 0)
	for _, name := range candidates {
		info, err := os.Stat(filepath.Join(sourceDir, name))
		if err == nil && info.Mode().IsRegular() {
			files = append(files, name)
		}
	}
	return files
}

func devSetupNextSteps(receipt devSetupReceipt, start bool) []string {
	if !receipt.Authenticated {
		return []string{"gregale login", "gregale dev setup --start"}
	}
	if receipt.Doctor.Errors > 0 {
		return []string{"fix the local source check errors above", "gregale dev setup --start"}
	}
	if start {
		return nil
	}
	return []string{devSetupShellCommand(receipt.StartCommand)}
}

func devSetupDevArgs(sourcePath, name, envFile string, once, noLogs, open, postgres bool, postgresRegion string) []string {
	args := make([]string, 0, 12)
	if sourcePath != "" {
		args = append(args, "--path", sourcePath)
	}
	if name != "" {
		args = append(args, "--name", name)
	}
	if envFile != "" {
		args = append(args, "--env-file", envFile)
	}
	if once {
		args = append(args, "--once")
	}
	if noLogs {
		args = append(args, "--no-logs")
	}
	if open {
		args = append(args, "--open")
	}
	if postgres {
		args = append(args, "--postgres")
		if postgresRegion != "" {
			args = append(args, "--postgres-region", postgresRegion)
		}
	}
	return args
}

func devSetupDevArgsForDisplay(cwd, sourceDir, project, envFile string, once, noLogs, open, postgres bool, postgresRegion string) []string {
	args := []string{"gregale", "dev"}
	rel, err := filepath.Rel(cwd, sourceDir)
	if err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
		args = append(args, "--path", filepath.ToSlash(rel))
	}
	if project != sanitizeSlug(filepath.Base(cwd)) || sourceDir != cwd {
		args = append(args, "--name", project)
	}
	if envFile != "" {
		envRel, relErr := filepath.Rel(cwd, envFile)
		if relErr == nil && !strings.HasPrefix(envRel, ".."+string(filepath.Separator)) {
			envFile = filepath.ToSlash(envRel)
		}
		args = append(args, "--env-file", envFile)
	}
	if once {
		args = append(args, "--once")
	}
	if noLogs {
		args = append(args, "--no-logs")
	}
	if open {
		args = append(args, "--open")
	}
	if postgres {
		args = append(args, "--postgres")
		if postgresRegion != "" {
			args = append(args, "--postgres-region", postgresRegion)
		}
	}
	return args
}

func devSetupShellCommand(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		if i == 0 || strings.IndexFunc(arg, func(r rune) bool { return r == ' ' || r == '\t' || r == '\'' || r == '"' }) < 0 {
			quoted[i] = arg
			continue
		}
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
	}
	return strings.Join(quoted, " ")
}

func renderDevSetup(stdout, stderr io.Writer, receipt devSetupReceipt) {
	if receipt.Ready {
		PrintOK(stdout, "Developer setup ready: %s", receipt.Project)
	} else {
		PrintWarn(stdout, "Developer setup needs attention: %s", receipt.Project)
	}
	PrintProgress(stdout, "source: %s", receipt.Path)
	detected := receipt.Class
	if receipt.Framework != "" {
		detected += " / " + receipt.Framework
	}
	if receipt.Runtime != "" {
		detected += " / " + receipt.Runtime
	}
	PrintProgress(stdout, "detected: %s", detected)
	if len(receipt.DependencyFiles) > 0 {
		PrintProgress(stdout, "dependency inputs: %s", strings.Join(receipt.DependencyFiles, ", "))
	}
	if receipt.EnvFile != "" {
		PrintProgress(stdout, "env file: %s (%d keys; values hidden)", receipt.EnvFile, receipt.EnvKeyCount)
	} else {
		PrintProgress(stdout, "env file: none selected (use --env-file .env.dev to opt in)")
	}
	if receipt.Hosting != nil && (receipt.Hosting.Port != 0 || receipt.Hosting.Health != "" || receipt.Hosting.Start != "") {
		PrintProgress(stdout, "hosting overrides: start=%q port=%d health=%s", receipt.Hosting.Start, receipt.Hosting.Port, receipt.Hosting.Health)
	}
	if !receipt.Authenticated {
		PrintWarn(stderr, "not logged in; run `gregale login` before starting")
	}
	if receipt.Doctor.Errors > 0 {
		PrintWarn(stderr, "local source checks: %d error(s), %d warning(s)", receipt.Doctor.Errors, receipt.Doctor.Warnings)
	}
	if len(receipt.Next) > 0 {
		PrintProgress(stdout, "Next:")
		for _, step := range receipt.Next {
			_, _ = fmt.Fprintf(stdout, "  %s\n", step)
		}
	}
}
