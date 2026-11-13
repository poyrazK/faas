package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/onebox-faas/faas/pkg/projectconfig"
)

// projectMigration is the outcome of moving .gregale/project.yaml into the
// root gregale.yaml `project:` block (ADR-20261107180000).
type projectMigration struct {
	Manifest      string `json:"manifest"`
	Legacy        string `json:"legacy"`
	CreatedFile   bool   `json:"created_manifest"`
	RemovedLegacy bool   `json:"removed_legacy"`
	DryRun        bool   `json:"dry_run"`
	Content       string `json:"content,omitempty"`
}

// cmdProjectsImport is local: it reads and writes the checkout only.
func cmdProjectsImport(args []string) int {
	flags, positional := splitArgsForFlags(args, "dry-run")
	fs := newFlagSet("projects-import", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "print the new gregale.yaml without writing")
	if err := fs.Parse(flags); err != nil || len(positional) > 1 {
		PrintUsage(os.Stderr, "usage: gregale project import [<dir>] [--dry-run]", "projects")
		return 1
	}
	dir := "."
	if len(positional) == 1 {
		dir = positional[0]
	}
	result, err := migrateProjectConfig(dir, *dryRun)
	if err != nil {
		return printErr("Could not migrate project settings", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	if result.DryRun {
		_, _ = fmt.Fprintf(osStdout, "Would write %s and remove %s:\n\n%s", result.Manifest, result.Legacy, result.Content)
		return 0
	}
	verb := "Added"
	if result.CreatedFile {
		verb = "Created"
	}
	_, _ = fmt.Fprintf(osStdout, "%s the `project:` block in %s and removed %s. Commit both changes.\n", verb, result.Manifest, result.Legacy)
	return 0
}

// migrateProjectConfig appends the legacy file to the root manifest as a
// `project:` block. It edits text rather than re-encoding YAML, so the
// manifest's existing formatting and comments are untouched, and it refuses
// to write unless the new manifest's block decodes to exactly the legacy
// settings.
func migrateProjectConfig(dir string, dryRun bool) (projectMigration, error) {
	legacyPath := filepath.Join(dir, filepath.FromSlash(projectconfig.LegacyPath))
	legacyBody, err := os.ReadFile(legacyPath)
	if errors.Is(err, fs.ErrNotExist) {
		return projectMigration{}, fmt.Errorf("%s not found; nothing to migrate", legacyPath)
	}
	if err != nil {
		return projectMigration{}, err
	}
	legacy, err := projectconfig.Parse(legacyBody)
	if err != nil {
		return projectMigration{}, fmt.Errorf("parse %s: %w", legacyPath, err)
	}
	if err := legacy.Validate(); err != nil {
		return projectMigration{}, fmt.Errorf("%s: %w", legacyPath, err)
	}
	manifestPath, existing, created, err := rootManifestForMigration(dir)
	if err != nil {
		return projectMigration{}, err
	}
	if !created {
		if _, found, err := projectconfig.FromManifest(existing); err != nil {
			return projectMigration{}, fmt.Errorf("parse %s: %w", manifestPath, err)
		} else if found {
			return projectMigration{}, fmt.Errorf("%s already has a `project:` block; merge %s into it by hand and delete the file", manifestPath, legacyPath)
		}
	}
	content := appendProjectBlock(existing, legacyBody)
	migrated, found, err := projectconfig.FromManifest([]byte(content))
	if err != nil || !found || !reflect.DeepEqual(normalizedProjectConfig(migrated), normalizedProjectConfig(legacy)) {
		return projectMigration{}, fmt.Errorf("the migrated block in %s would not match %s; move it by hand", manifestPath, legacyPath)
	}
	result := projectMigration{Manifest: manifestPath, Legacy: legacyPath, CreatedFile: created, DryRun: dryRun}
	if dryRun {
		result.Content = content
		return result, nil
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(manifestPath); err == nil {
		mode = info.Mode().Perm()
	}
	if err := writeProjectManifestAtomic(manifestPath, []byte(content), mode); err != nil {
		return projectMigration{}, fmt.Errorf("write %s: %w", manifestPath, err)
	}
	if err := os.Remove(legacyPath); err != nil {
		return projectMigration{}, fmt.Errorf("%s now holds the settings, but removing %s failed (delete it before the next deploy): %w", manifestPath, legacyPath, err)
	}
	result.RemovedLegacy = true
	return result, nil
}

// rootManifestForMigration picks the root gregale.yaml (or .yml). A root
// gregale.toml cannot take the YAML block.
func rootManifestForMigration(dir string) (path string, body []byte, created bool, err error) {
	for _, name := range projectconfig.ManifestNames {
		path = filepath.Join(dir, name)
		body, err = os.ReadFile(path)
		if err == nil {
			return path, body, false, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", nil, false, err
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "gregale.toml")); err == nil {
		return "", nil, false, fmt.Errorf("the root manifest is gregale.toml; the `project:` block needs gregale.yaml, so move the settings by hand")
	}
	return filepath.Join(dir, projectconfig.ManifestNames[0]), nil, true, nil
}

// appendProjectBlock nests the legacy document under `project:` by indenting
// every line two spaces. Document markers are dropped; blank lines stay blank.
func appendProjectBlock(manifest, legacy []byte) string {
	var b strings.Builder
	b.Write(manifest)
	if len(manifest) > 0 && !strings.HasSuffix(string(manifest), "\n") {
		b.WriteByte('\n')
	}
	if len(manifest) > 0 {
		b.WriteByte('\n')
	}
	b.WriteString("# Project-wide service settings (moved from .gregale/project.yaml).\n")
	b.WriteString(projectconfig.ManifestKey + ":\n")
	for _, line := range strings.Split(strings.TrimRight(string(legacy), "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" || trimmed == "..." {
			continue
		}
		if trimmed == "" {
			b.WriteByte('\n')
			continue
		}
		b.WriteString("  " + strings.TrimRight(line, "\r") + "\n")
	}
	return b.String()
}

// writeProjectManifestAtomic replaces path through a temporary file in the
// same directory, so an interrupted write never leaves a truncated manifest.
func writeProjectManifestAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".gregale-yaml-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, err = tmp.Write(data)
	if chmodErr := tmp.Chmod(mode); err == nil {
		err = chmodErr
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}

// normalizedProjectConfig treats nil and empty collections alike for the
// equality check.
func normalizedProjectConfig(cfg projectconfig.Config) projectconfig.Config {
	if len(cfg.Services) == 0 {
		cfg.Services = nil
	}
	for name, service := range cfg.Services {
		if len(service.DependsOn) == 0 {
			service.DependsOn = nil
		}
		cfg.Services[name] = service
	}
	return cfg
}
