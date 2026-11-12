package main

// `gregale sandbox template` (ADR-20261027090013 step 10, part three): build a
// sandbox template from an E2B-style template directory. The directory's
// e2b.toml names the template and its start and ready commands, and its
// e2b.Dockerfile is built in a builder microVM by deploying the directory to
// the template's platform-managed build app.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	sandboxTemplatesUsage      = "usage: gregale sandbox template <build|list|get|rm> [args]"
	sandboxTemplateBuildUsage  = "usage: gregale sandbox template publish [<dir>] [--name NAME] [--dockerfile FILE] [--start-cmd CMD] [--ready-cmd CMD] [--ready-timeout S] [--no-wait] [--wait-timeout D]"
	sandboxTemplateConfigFile  = "e2b.toml"
	sandboxTemplateDockerfile  = "e2b.Dockerfile"
	sandboxTemplatePollDefault = 3 * time.Second
)

// sandboxTemplatePoll is how often build waits poll; tests shorten it.
var sandboxTemplatePoll = sandboxTemplatePollDefault

// sandboxTemplateConfig is the part of e2b.toml Gregale reads. cpu_count and
// memory_mb are session sizes here (preset sizes), so they are ignored.
type sandboxTemplateConfig struct {
	TemplateName string `toml:"template_name"`
	Dockerfile   string `toml:"dockerfile"`
	StartCmd     string `toml:"start_cmd"`
	ReadyCmd     string `toml:"ready_cmd"`
}

func cmdSandboxTemplates(args []string) int {
	parent, _ := lookupCliCommand("sandbox-templates")
	if len(args) == 0 {
		PrintUsage(os.Stderr, sandboxTemplatesUsage, "sandbox-templates")
		return 1
	}
	switch args[0] {
	case "build":
		return cmdSandboxTemplateBuild(args[1:])
	case subList:
		return cmdSandboxTemplateList(args[1:])
	case subGet:
		return cmdSandboxTemplateGet(args[1:])
	case subRm:
		return cmdSandboxTemplateRm(args[1:])
	}
	printCommandValidation(os.Stderr, "unknown sandbox template subcommand %q\n", args[0])
	return unknownSubcommandChoices(os.Stderr, "sandbox-templates", parent, args[0])
}

type sandboxTemplateBuildOpts struct {
	dir, name, dockerfile string
	req                   api.SandboxTemplateRequest
	wait                  bool
	waitTimeout           time.Duration
}

func parseSandboxTemplateBuild(args []string) (sandboxTemplateBuildOpts, bool) {
	flags, positional := splitArgsForFlags(args, "no-wait")
	fs := newFlagSet("sandbox template publish", flag.ContinueOnError)
	name := fs.String("name", "", "template name (default: e2b.toml template_name, else the directory name)")
	dockerfile := fs.String("dockerfile", "", "Dockerfile inside the directory (default: e2b.toml dockerfile, else e2b.Dockerfile, else Dockerfile)")
	startCmd := fs.String("start-cmd", "", "command a session runs in the background at boot (default: e2b.toml start_cmd)")
	readyCmd := fs.String("ready-cmd", "", "command polled until it exits 0 before a session is ready (default: e2b.toml ready_cmd)")
	readyTimeout := fs.Int("ready-timeout", 0, "seconds the ready command may take (default 60)")
	noWait := fs.Bool("no-wait", false, "queue the build and return")
	waitTimeout := fs.Duration("wait-timeout", 20*time.Minute, "maximum time to wait for the build")
	if err := fs.Parse(flags); err != nil {
		return sandboxTemplateBuildOpts{}, false
	}
	if rejectUnexpectedFlagArgs(fs) || len(positional) > 1 {
		PrintUsage(os.Stderr, sandboxTemplateBuildUsage, "sandbox-templates")
		return sandboxTemplateBuildOpts{}, false
	}
	opts := sandboxTemplateBuildOpts{dir: ".", wait: !*noWait, waitTimeout: *waitTimeout}
	if len(positional) == 1 {
		opts.dir = positional[0]
	}
	cfg, err := loadSandboxTemplateConfig(opts.dir)
	if err != nil {
		printErr("Could not read "+sandboxTemplateConfigFile, err)
		return sandboxTemplateBuildOpts{}, false
	}
	opts.name = firstNonEmpty(*name, cfg.TemplateName, sandboxTemplateDirName(opts.dir))
	opts.dockerfile = firstNonEmpty(*dockerfile, cfg.Dockerfile)
	opts.req = api.SandboxTemplateRequest{StartCmd: firstNonEmpty(*startCmd, cfg.StartCmd), ReadyCmd: firstNonEmpty(*readyCmd, cfg.ReadyCmd), ReadyTimeoutS: *readyTimeout}
	if !api.ValidSandboxTemplateName(opts.name) {
		printErr("Invalid template name", fmt.Errorf("%q: use 1-63 lowercase letters, digits and inner hyphens (--name)", opts.name))
		return sandboxTemplateBuildOpts{}, false
	}
	return opts, true
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func sandboxTemplateDirName(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	return strings.ToLower(filepath.Base(abs))
}

// loadSandboxTemplateConfig reads e2b.toml when the directory has one.
func loadSandboxTemplateConfig(dir string) (sandboxTemplateConfig, error) {
	var cfg sandboxTemplateConfig
	data, err := os.ReadFile(filepath.Join(dir, sandboxTemplateConfigFile))
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", sandboxTemplateConfigFile, err)
	}
	return cfg, nil
}

// sandboxTemplateDockerfilePath picks the Dockerfile relative to dir: the
// named one, else e2b.Dockerfile, else Dockerfile. It must be a regular file
// inside the directory.
func sandboxTemplateDockerfilePath(dir, named string) (string, error) {
	candidates := []string{sandboxTemplateDockerfile, "Dockerfile"}
	if named != "" {
		candidates = []string{named}
	}
	for _, rel := range candidates {
		clean := filepath.Clean(filepath.FromSlash(rel))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("the Dockerfile %q must be inside the template directory", rel)
		}
		info, err := os.Lstat(filepath.Join(dir, clean))
		if errors.Is(err, fs.ErrNotExist) && named == "" {
			continue
		}
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("the Dockerfile %q is not a regular file", rel)
		}
		return filepath.ToSlash(clean), nil
	}
	return "", fmt.Errorf("no %s or Dockerfile in %s (--dockerfile)", sandboxTemplateDockerfile, dir)
}

// packSandboxTemplate archives dir for the build app with the template's
// Dockerfile at the archive root as Dockerfile, the name the builder reads.
func packSandboxTemplate(dir, dockerfile string) (string, int, error) {
	overrides, findings, err := scanAndRedactEnvFiles(dir, modeWarn)
	if err != nil {
		return "", 0, err
	}
	renderSecretScanWarnings(findings, osStderr)
	var buildOnly map[string][]byte
	var excludes []string
	if dockerfile != "Dockerfile" {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(dockerfile)))
		if err != nil {
			return "", 0, err
		}
		buildOnly = map[string][]byte{"Dockerfile": data}
		if _, err := os.Lstat(filepath.Join(dir, "Dockerfile")); err == nil {
			excludes = append(excludes, filepath.Join(dir, "Dockerfile"))
		}
	}
	f, err := os.CreateTemp("", "gregale-sandbox-template-*.tar.gz")
	if err != nil {
		return "", 0, err
	}
	path := f.Name()
	_ = f.Close()
	n, err := packDirToTarGzWithRoot(dir, path, largestSourceTarballMB(), overrides, "", buildOnly, true, excludes...)
	if err != nil {
		_ = os.Remove(path)
		return "", 0, err
	}
	return path, n, nil
}

func cmdSandboxTemplateBuild(args []string) int {
	opts, ok := parseSandboxTemplateBuild(args)
	if !ok {
		return 1
	}
	dockerfile, err := sandboxTemplateDockerfilePath(opts.dir, opts.dockerfile)
	if err != nil {
		return printErr("Invalid template directory", err)
	}
	client, err := authedClientWithDeployTimeout(5 * time.Minute)
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	tpl, err := client.PutSandboxTemplate(ctx, opts.name, opts.req)
	if err != nil {
		return printErr("Could not save the template", err)
	}
	tarball, files, err := packSandboxTemplate(opts.dir, dockerfile)
	if err != nil {
		return printErr("Could not pack the template directory", err)
	}
	defer func() { _ = os.Remove(tarball) }()
	if !jsonOutput {
		PrintProgress(osStderr, "building template %s from %s (%d file(s))", opts.name, dockerfile, files)
	}
	dep, err := DeployTarball(client, ctx, tpl.App, tarball, "", "", true, api.DeployAnnotations{})
	if err != nil {
		return printErr("Could not start the template build", err)
	}
	if !opts.wait {
		if jsonOutput {
			return jsonOut(writeJSON(tpl))
		}
		PrintOK(osStdout, "Template %s build %s queued; check it with `gregale sandbox template get %s`", opts.name, dep.ID, opts.name)
		return 0
	}
	return waitSandboxTemplateBuild(ctx, client, opts.name, dep.ID, opts.waitTimeout)
}

// waitSandboxTemplateBuild polls the template until build buildID ends.
func waitSandboxTemplateBuild(ctx context.Context, client *Client, name, buildID string, timeout time.Duration) int {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		tpl, err := client.GetSandboxTemplate(ctx, name)
		if err != nil && ctx.Err() == nil {
			return printErr("Could not read the template", err)
		}
		if err == nil && tpl.Build != nil && tpl.Build.ID == buildID && isTerminalDeploymentStatus(tpl.Build.Status) {
			if jsonOutput {
				_ = writeJSON(tpl)
			}
			if tpl.Build.Status != statusLive {
				return printErr("Template build failed", fmt.Errorf("build %s %s: %s (details: gregale deployment get %s)", buildID, tpl.Build.Status, firstNonEmpty(tpl.Build.Error, "no error recorded"), buildID))
			}
			if !jsonOutput {
				PrintOK(osStdout, "Template %s is ready; start a session with {\"template\":%q}", name, name)
			}
			return 0
		}
		select {
		case <-ctx.Done():
			return printErr("Template build still running", fmt.Errorf("gave up after %s; check it with `gregale sandbox template get %s`", timeout, name))
		case <-time.After(sandboxTemplatePoll):
		}
	}
}

func cmdSandboxTemplateList(args []string) int {
	if len(args) != 0 {
		PrintUsage(os.Stderr, "usage: gregale sandbox template list", "sandbox-templates")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.ListSandboxTemplates(context.Background())
	if err != nil {
		return printErr("Could not list sandbox templates", err)
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(out.Items))
	}
	if len(out.Items) == 0 {
		printEmptyResult("sandbox templates", "", "gregale sandbox template publish ./my-template")
		return 0
	}
	for _, t := range out.Items {
		_, _ = fmt.Fprintf(osStdout, "%-32s %-10s %s\n", t.Name, sandboxTemplateState(t), sandboxTemplateBuiltAt(t))
	}
	return 0
}

func cmdSandboxTemplateGet(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: gregale sandbox template get <name>", "sandbox-templates")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	t, err := client.GetSandboxTemplate(context.Background(), args[0])
	if err != nil {
		return printErr("Could not read the template", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(t))
	}
	_, _ = fmt.Fprintf(osStdout, "name:      %s\nstate:     %s\nbuilt:     %s\n", t.Name, sandboxTemplateState(t), sandboxTemplateBuiltAt(t))
	if t.Build != nil {
		_, _ = fmt.Fprintf(osStdout, "build:     %s %s\n", t.Build.ID, t.Build.Status)
		if t.Build.Error != "" {
			_, _ = fmt.Fprintf(osStdout, "error:     %s\n", t.Build.Error)
		}
	}
	if t.StartCmd != "" {
		_, _ = fmt.Fprintf(osStdout, "start_cmd: %s\n", t.StartCmd)
	}
	if t.ReadyCmd != "" {
		_, _ = fmt.Fprintf(osStdout, "ready_cmd: %s (timeout %ds)\n", t.ReadyCmd, t.ReadyTimeoutS)
	}
	return 0
}

func cmdSandboxTemplateRm(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: gregale sandbox template delete <name>", "sandbox-templates")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteSandboxTemplate(context.Background(), args[0]); err != nil {
		return printErr("Could not delete the template", err)
	}
	if !jsonOutput {
		PrintOK(osStdout, "Deleted sandbox template %s", args[0])
	}
	return 0
}

// sandboxTemplateState is ready, building, failed or new.
func sandboxTemplateState(t api.SandboxTemplate) string {
	switch {
	case t.Build != nil && !isTerminalDeploymentStatus(t.Build.Status):
		return "building"
	case t.Ready:
		return "ready"
	case t.Build != nil:
		return t.Build.Status
	default:
		return "new"
	}
}

func sandboxTemplateBuiltAt(t api.SandboxTemplate) string {
	if t.BuiltAt == nil {
		return "-"
	}
	return t.BuiltAt.UTC().Format(time.RFC3339)
}
