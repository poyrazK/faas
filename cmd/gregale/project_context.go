package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	projectContextDirName  = ".gregale"
	projectContextFileName = "project.json"
	projectContextVersion  = 1
)

var errProjectContextNotFound = errors.New("no linked Gregale project context")

// localProjectContext is deliberately non-secret. It identifies the remote
// project and, when a project has one or an explicitly selected workload, the
// app that app-scoped commands should target. The file lives in the checkout,
// not in the global CLI config, so a developer can work on several projects
// without carrying the last project's target across directories.
type localProjectContext struct {
	Version     int    `json:"version"`
	Project     string `json:"project"`
	App         string `json:"app,omitempty"`
	Environment string `json:"environment,omitempty"`
}

type projectContextReceipt struct {
	Context localProjectContext `json:"context"`
	Path    string              `json:"path"`
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
	if c.Version != projectContextVersion {
		return fmt.Errorf("unsupported version %d (supported: %d)", c.Version, projectContextVersion)
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
	if root, err := gitRootFromCwd(start); err == nil {
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

func linkedAppSlug(start string) (string, error) {
	context, _, err := linkedProjectContext(start)
	if err != nil {
		return "", err
	}
	if context.App == "" {
		return "", fmt.Errorf("project %q has multiple or no workloads; link again with --app <slug>", context.Project)
	}
	return context.App, nil
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
	_, _ = fmt.Fprintf(w, "Project:     %s\n", receipt.Context.Project)
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
		return deriveName(), nil
	}
	if err != nil {
		return "", err
	}
	return app, nil
}
