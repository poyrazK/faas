package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Successful onboarding remembers a default next to the selected source.
// It must not replace a different target or change an ancestor's other apps.
func (r *startRunner) rememberApp() error {
	if r.session.DeploymentID == "" || r.session.AppID == "" {
		return nil
	}
	info, err := os.Stat(r.session.SourcePath)
	if err != nil || !info.IsDir() {
		return errors.New("source directory is unavailable; use an explicit app until it is restored")
	}
	before, key, err := startSourceContext(r.session.SourcePath)
	if err != nil {
		return err
	}
	if key != r.session.SourceContext {
		return errors.New("saved context changed during this launch; existing defaults were kept")
	}
	if before.App != "" {
		if before.App != r.session.AppSlug || before.Project != r.session.Project || before.Environment != r.session.Environment {
			return errors.New("a different app is already saved; use gregale context to review it")
		}
		return nil
	}
	link := localProjectContext{Version: projectContextVersion, Project: r.session.Project, App: r.session.AppSlug, Environment: r.session.Environment}
	if link.Project == "" {
		link.Version = standaloneContextVersion
	}
	for _, name := range []string{projectContextDirName, ".gitignore", filepath.Join(projectContextDirName, projectContextFileName)} {
		info, err := os.Lstat(filepath.Join(r.session.SourcePath, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (name != projectContextDirName && !info.Mode().IsRegular()) {
			return fmt.Errorf("refusing to save defaults through non-regular %s", name)
		}
	}
	if err := ensureProjectContextIgnored(r.session.SourcePath); err != nil {
		return fmt.Errorf("ignore local context: %w", err)
	}
	path, err := saveProjectContext(r.session.SourcePath, link)
	if err != nil {
		return err
	}
	// Keep the accepted deployment recoverable after saving this new default.
	_, r.session.SourceContext, err = startSourceContext(r.session.SourcePath)
	if err != nil {
		return err
	}
	_, r.session.ContextKey, err = startSourceContext(r.session.ScopePath)
	if err != nil {
		return err
	}
	base, err := startSessionPath(r.session.ScopePath)
	if err != nil {
		return err
	}
	r.statePath = startContextSessionPath(base, r.session.APIBase, r.session.AccountID, r.session.ContextKey)
	if err := saveStartSession(r.statePath, r.session); err != nil {
		return fmt.Errorf("app default saved but recovery record could not be updated: %w", err)
	}
	PrintOK(osStdout, "Remembered app %s in %s", link.App, path)
	return nil
}

func validateStartScopeBeforeLogin(scope, statePath string) error {
	if _, err := os.ReadDir(scope); err != nil {
		// Accepted and pending submissions can be recovered after source removal.
		paths, globErr := filepath.Glob(strings.TrimSuffix(statePath, ".json") + "-*.json")
		if globErr != nil {
			return globErr
		}
		for _, path := range append(paths, statePath) {
			saved, found, loadErr := loadStartSession(path)
			if loadErr == nil && found && saved.ScopePath == scope && (saved.DeploymentID != "" || saved.PendingSubmission != nil) {
				return nil
			}
		}
		return err
	}
	return nil
}
