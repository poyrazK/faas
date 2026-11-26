package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/projectconfig"
)

const (
	projectContextDirName    = ".gregale"
	projectContextFileName   = "project.json"
	projectContextVersion    = 1
	standaloneContextVersion = 2
)

var errProjectContextNotFound = errors.New("no linked Gregale project context")

// Set only by TestMain: tests must neither inherit nor overwrite a developer's
// checkout context when their temporary directories have linked ancestors.
var testOnlyProjectContextRoot string

func projectContextRootAllowed(path string) bool {
	if testOnlyProjectContextRoot == "" {
		return true
	}
	rel, err := filepath.Rel(testOnlyProjectContextRoot, path)
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}

// localProjectContext is deliberately non-secret. It identifies the remote
// project and, when a project has one or an explicitly selected workload, the
// app that app-scoped commands should target. The file lives in the checkout,
// not in the global CLI config, so a developer can work on several projects
// without carrying the last project's target across directories.
type localProjectContext struct {
	Version     int    `json:"version"`
	Project     string `json:"project,omitempty"`
	App         string `json:"app,omitempty"`
	Environment string `json:"environment,omitempty"`
}

type projectContextReceipt struct {
	Context      localProjectContext       `json:"context"`
	Path         string                    `json:"path"`
	Connection   *connectionContext        `json:"connection,omitempty"`
	Targeting    *projectTargetingOverview `json:"targeting,omitempty"`
	Verification *contextVerification      `json:"verification,omitempty"`
}

func projectContextPath(root string) string {
	return filepath.Join(root, projectContextDirName, projectContextFileName)
}

// findProjectContext walks from start to the filesystem root. This makes a
// linked repository usable from an app subdirectory while keeping discovery
// deterministic: the nearest context wins.
func findProjectContext(start string) (localProjectContext, string, error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return localProjectContext{}, "", fmt.Errorf("resolve project context directory: %w", err)
	}
	info, err := os.Stat(abs)
	if err == nil && !info.IsDir() {
		abs = filepath.Dir(abs)
	}
	for {
		if !projectContextRootAllowed(abs) {
			return localProjectContext{}, "", errProjectContextNotFound
		}
		path := projectContextPath(abs)
		b, readErr := os.ReadFile(path)
		if readErr == nil {
			var context localProjectContext
			decoder := json.NewDecoder(strings.NewReader(string(b)))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&context); err != nil {
				return localProjectContext{}, path, fmt.Errorf("parse %s: %w", path, err)
			}
			if err := context.validate(); err != nil {
				return localProjectContext{}, path, fmt.Errorf("validate %s: %w", path, err)
			}
			return context, path, nil
		}
		if !errors.Is(readErr, os.ErrNotExist) {
			return localProjectContext{}, path, fmt.Errorf("read %s: %w", path, readErr)
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return localProjectContext{}, "", errProjectContextNotFound
		}
		abs = parent
	}
}

func (c localProjectContext) validate() error {
	// Version 2 adds standalone apps. Older CLIs refuse it instead of
	// silently deriving a different target; project links retain version 1.
	if c.Version == standaloneContextVersion {
		if c.Project != "" || c.Environment != "" || !api.ValidAppSlug(c.App) {
			return errors.New("standalone context requires an app and no project or environment; use gregale unlink to clear it or gregale link to select a project")
		}
		return nil
	}
	if c.Version != projectContextVersion {
		return fmt.Errorf("unsupported version %d (supported: 1 and 2)", c.Version)
	}
	if !api.ValidProjectSlug(c.Project) {
		return fmt.Errorf("project %q is not a valid project slug", c.Project)
	}
	if c.App != "" && !api.ValidAppSlug(c.App) {
		return fmt.Errorf("app %q is not a valid app slug", c.App)
	}
	if c.Environment != "" && !api.ValidProjectEnvironmentSlug(c.Environment) {
		return fmt.Errorf("environment %q is not a valid project environment slug", c.Environment)
	}
	return nil
}

func projectContextRoot(start string) (string, error) {
	if _, path, err := findProjectContext(start); err == nil {
		return filepath.Dir(filepath.Dir(path)), nil
	} else if !errors.Is(err, errProjectContextNotFound) {
		return "", err
	}
	if root, err := gitRootFromCwd(start); err == nil && projectContextRootAllowed(root) {
		return root, nil
	}
	return filepath.Abs(start)
}

func saveProjectContext(root string, context localProjectContext) (string, error) {
	if err := context.validate(); err != nil {
		return "", err
	}
	dir := filepath.Join(root, projectContextDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	b, err := json.MarshalIndent(context, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode project context: %w", err)
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(dir, ".project-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create project context temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return "", fmt.Errorf("secure project context: %w", err)
	}
	if _, err := tmp.Write(b); err != nil {
		cleanup()
		return "", fmt.Errorf("write project context: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return "", fmt.Errorf("sync project context: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("close project context: %w", err)
	}
	path := projectContextPath(root)
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("save project context: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", fmt.Errorf("secure project context: %w", err)
	}
	return path, nil
}

func ensureProjectContextIgnored(root string) error {
	path := filepath.Join(root, ".gitignore")
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		b = nil
	} else if err != nil {
		return err
	}
	for _, line := range strings.Split(string(b), "\n") {
		switch strings.TrimSpace(line) {
		case ".gregale", ".gregale/", "/.gregale", "/.gregale/":
			return nil
		}
	}
	content := string(b)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += "\n# Gregale local project context\n.gregale/\n"
	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}
	return os.WriteFile(path, []byte(content), mode)
}

func linkedProjectContext(start string) (localProjectContext, string, error) {
	return findProjectContext(start)
}

// otherAppLinkedAbove reports whether dir's nearest saved context is an
// ancestor directory's standalone link to an app other than slug. That link
// is not dir's own: the nearest context wins, so a link saved in dir itself
// targets slug without rewriting the ancestor's, which the directories
// beside dir still use. On prod (hunt r50) `deploy --name` under such a
// directory saved no link, so later commands there targeted the other app,
// and `link --app` overwrote the ancestor's link.
func otherAppLinkedAbove(dir, slug string) bool {
	link, path, err := findProjectContext(dir)
	if err != nil || link.Version != standaloneContextVersion || link.App == slug {
		return false
	}
	abs, err := filepath.Abs(dir)
	return err == nil && filepath.Dir(filepath.Dir(path)) != abs
}

// targetProjectContext is the checkout's CLI target for commands that only
// read it (ADR-20261107180000 slice 3). The local .gregale/project.json wins;
// without one, the committed gregale.yaml `project:` block's name and app
// apply, so a fresh clone targets the right project without `gregale link`.
// The returned path names the file the target came from. Commands that
// rewrite or delete the link file (link, unlink, context set) keep using
// findProjectContext, which never returns a manifest.
func targetProjectContext(start string) (localProjectContext, string, error) {
	context, path, err := findProjectContext(start)
	if !errors.Is(err, errProjectContextNotFound) {
		return context, path, err
	}
	return committedProjectContext(start)
}

// committedProjectContext walks from start towards the repository root and
// takes the nearest manifest's project.app and the nearest project.name, so a
// service directory's gregale.yaml can name its app while the root names the
// project. The walk stops at the Git root (or the filesystem root).
func committedProjectContext(start string) (localProjectContext, string, error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return localProjectContext{}, "", fmt.Errorf("resolve project context directory: %w", err)
	}
	if info, statErr := os.Stat(abs); statErr == nil && !info.IsDir() {
		abs = filepath.Dir(abs)
	}
	var project, app, source string
	for projectContextRootAllowed(abs) {
		cfg, path, found, err := manifestTargetIn(abs)
		if err != nil {
			return localProjectContext{}, path, err
		}
		if found {
			if app == "" && cfg.App != "" {
				app = cfg.App
				if source == "" {
					source = path
				}
			}
			if cfg.Name != "" {
				project = cfg.Name
				if source == "" {
					source = path
				}
				break
			}
		}
		if _, err := os.Stat(filepath.Join(abs, ".git")); err == nil {
			break
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			break
		}
		abs = parent
	}
	if project == "" && app == "" {
		return localProjectContext{}, "", errProjectContextNotFound
	}
	context := localProjectContext{Version: projectContextVersion, Project: project, App: app}
	if project == "" {
		context.Version = standaloneContextVersion
	}
	if err := context.validate(); err != nil {
		return localProjectContext{}, source, fmt.Errorf("validate %s project: name/app: %w", source, err)
	}
	return context, source, nil
}

// manifestTargetIn reads the project block of dir's gregale.yaml (or .yml).
func manifestTargetIn(dir string) (projectconfig.Config, string, bool, error) {
	for _, name := range projectconfig.ManifestNames {
		path := filepath.Join(dir, name)
		body, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return projectconfig.Config{}, path, false, fmt.Errorf("read %s: %w", path, err)
		}
		cfg, found, err := projectconfig.FromManifest(body)
		if err != nil {
			return projectconfig.Config{}, path, false, fmt.Errorf("parse %s: %w", path, err)
		}
		return cfg, path, found, nil
	}
	return projectconfig.Config{}, "", false, nil
}

func linkedAppSlug(start string) (string, error) {
	context, _, err := targetProjectContext(start)
	if err != nil {
		return "", err
	}
	if context.App == "" {
		// The fix is usually one flag on this command; "link again" read as
		// the only way out in a Compose checkout (prod hunt r27).
		return "", fmt.Errorf("this checkout is linked to project %q, which has multiple or no workloads; name one app: pass --app <slug>, or link one with %s link %s --app <slug>", context.Project, recoveryCLI(), context.Project)
	}
	return context.App, nil
}

// linkedProjectSlug returns the linked project's slug, or "" when there is
// no link or the link is a standalone app.
func linkedProjectSlug(start string) (string, error) {
	context, _, err := linkedProjectContext(start)
	if errors.Is(err, errProjectContextNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return context.Project, nil
}

func resolveLinkedApp(explicit, start string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	return linkedAppSlug(start)
}

func resolveAppFlagOrContext(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return resolveLinkedApp("", cwd)
}

// resolveEnvironmentFlagOrContext gives environment-aware commands the same
// zero-config behavior as app-scoped commands. An explicitly supplied flag
// always wins; an absent linked context simply preserves the command's
// existing default.
func resolveEnvironmentFlagOrContext(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return resolveEnvironmentFromContext(explicit, cwd)
}

func resolveEnvironmentFromContext(explicit, sourceDir string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	context, _, err := linkedProjectContext(sourceDir)
	if errors.Is(err, errProjectContextNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return context.Environment, nil
}

// appSlugFlagUsage is the help text every slug-first leaf gives --app.
const appSlugFlagUsage = "app slug (same as the <slug> positional; defaults to linked context)"

var errAppFlagConflict = errors.New("pass the app once: the <slug> positional and --app name different apps")

// mergeAppFlag lets a slug-first leaf take --app as well as its leading
// positional. production-us hunt #4: `gregale link` tells users to pass
// --app for app-scoped commands, yet ps, logs, metrics, slo, inspect and
// other slug-first leaves rejected it with "flag provided but not defined:
// -app". maxPositionals is the leaf's full positional count including the
// slug: when the caller already supplied all of them, the first must be the
// same app; otherwise the flag value is the omitted leading slug.
func mergeAppFlag(positional []string, app string, maxPositionals int) ([]string, error) {
	if app == "" {
		return positional, nil
	}
	if len(positional) >= maxPositionals {
		if positional[0] != app {
			return nil, errAppFlagConflict
		}
		return positional, nil
	}
	return append([]string{app}, positional...), nil
}

// withLinkedApp puts the linked app in front of a command's positionals when
// exactly its leading <slug> is missing: the command takes n positionals and
// got n-1. Commands whose --app says "defaults to linked context" call it
// after mergeAppFlag; mergeAppFlag alone never read the link, so `queue send`
// and `dlq list` in a linked checkout printed their usage (prod hunt r34).
// With no link the positionals are returned as they are.
//
// The positionals after <slug> are ids (a queue row): a lone non-id may be
// the slug itself with the id forgotten (`queue ack billing`, or `--app
// billing` merged in), and filling the link in front of it targeted the
// wrong app, so the command prints its usage instead.
func withLinkedApp(positional []string, n int) []string {
	if len(positional) != n-1 {
		return positional
	}
	for _, p := range positional {
		if uuid.Validate(p) != nil {
			return positional
		}
	}
	app := linkedAppForMissingSlug()
	if app == "" {
		return positional
	}
	return append([]string{app}, positional...)
}

// linkedAppAndEnvironment is the linked app a command (domain add/setup, cron
// create) targets when --app is missing, with the linked environment unless
// --environment names one: a checkout linked to staging bound its domain to
// production otherwise.
func linkedAppAndEnvironment(environment string) (string, string) {
	app := linkedAppForMissingSlug()
	if app == "" || environment != "" {
		return app, environment
	}
	if linked, err := resolveEnvironmentFlagOrContext(""); err == nil {
		environment = linked
	}
	return app, environment
}

// linkedAppForMissingSlug is the linked app for a command whose <slug> is
// missing, or "" without one. A link that names no single app (a project
// with several workloads, an unreadable gregale.yaml) says so on stderr: the
// usage line that follows does not explain why the link was not used.
func linkedAppForMissingSlug() string {
	app, err := resolveAppFlagOrContext("")
	if err != nil {
		if !errors.Is(err, errProjectContextNotFound) {
			_, _ = fmt.Fprintf(osStderr, "Linked app unavailable: %v\n", err)
		}
		return ""
	}
	return app
}

// resolveRequiredAppSlug gives app-scoped read commands a consistent
// context fallback. Callers own their usage text so each PrintUsage call
// keeps a statically discoverable docs topic. Explicit slugs still win
// without touching the filesystem.
func resolveRequiredAppSlug(explicit string) (string, error) {
	return resolveAppFlagOrContext(explicit)
}

func displayProjectContextPath(cwd, path string) string {
	rel, err := filepath.Rel(cwd, path)
	if err == nil && rel != "" && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
		return filepath.ToSlash(rel)
	}
	return path
}

func renderProjectContext(w io.Writer, receipt projectContextReceipt) {
	if jsonOutput {
		_ = writeJSON(receipt)
		return
	}
	if receipt.Context.Project == "" {
		_, _ = fmt.Fprintln(w, "Project:     (standalone app)")
	} else {
		_, _ = fmt.Fprintf(w, "Project:     %s\n", receipt.Context.Project)
	}
	if receipt.Context.App != "" {
		_, _ = fmt.Fprintf(w, "App:         %s\n", receipt.Context.App)
	} else {
		_, _ = fmt.Fprintln(w, "App:         (select with --app for app-scoped commands)")
	}
	if receipt.Context.Environment != "" {
		_, _ = fmt.Fprintf(w, "Environment: %s\n", receipt.Context.Environment)
	}
	_, _ = fmt.Fprintf(w, "Context:     %s\n", receipt.Path)
}

func linkedDefaultAppName(start string) (string, error) {
	app, err := linkedAppSlug(start)
	if errors.Is(err, errProjectContextNotFound) {
		return sanitizeSlug(filepath.Base(start)), nil
	}
	if err != nil {
		return "", err
	}
	return app, nil
}

// resolveSecretApp shares the linked-app rules used by env push while adding
// an actionable error for commands that require exactly one target.
func resolveSecretApp(explicit string) (string, error) {
	app, err := resolveAppFlagOrContext(explicit)
	if err != nil {
		return "", fmt.Errorf("%w; pass --app <slug> or run gregale link <project-slug> --app <slug>", err)
	}
	return app, nil
}

// fillLinkedApp defaults an empty --app to the app linked in this checkout,
// leaving it empty when there is none so the caller's usage error still fires.
func fillLinkedApp(slug *string) {
	if slug == nil || *slug != "" {
		return
	}
	if linked, err := resolveAppFlagOrContext(""); err == nil {
		*slug = linked
	}
}

// linkedProjectOnly reports whether start's checkout is linked to a project
// without naming one of its apps, the link a project deploy writes.
func linkedProjectOnly(start string) bool {
	context, _, err := targetProjectContext(start)
	return err == nil && context.Project != "" && context.App == ""
}

// linkedProjectOnlyHere reports whether the working directory's checkout is
// linked to a project without naming one app.
func linkedProjectOnlyHere() bool {
	cwd, err := os.Getwd()
	return err == nil && linkedProjectOnly(cwd)
}
