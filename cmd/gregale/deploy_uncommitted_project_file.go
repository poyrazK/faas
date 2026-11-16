package main

import (
	"path/filepath"
	"slices"
	"strings"
)

// uncommittedProjectFile names the Compose file or gregale.yaml at the
// deployed source root that `git status --porcelain` lists, and whether it is
// untracked. A HEAD deploy ships without an untracked one, so a Compose
// project planned zero workloads and the dry run said only "scan produced zero
// workloads; reconcile refused" (microservices-demo, 2026-11-16).
func uncommittedProjectFile(porcelain, repoRoot, sourceDir string) (string, bool) {
	rel, err := filepath.Rel(repoRoot, sourceDir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	names := append(slices.Clone(composeFileNames), "gregale.yaml")
	for _, line := range strings.Split(porcelain, "\n") {
		if len(line) < 4 {
			continue
		}
		status, path := line[:2], strings.Trim(line[3:], `"`)
		if _, renamed, ok := strings.Cut(path, " -> "); ok {
			path = renamed
		}
		dir, base := filepath.Split(filepath.FromSlash(path))
		if filepath.Clean(dir) != filepath.Clean(rel) || !slices.Contains(names, base) {
			continue
		}
		return filepath.ToSlash(path), status == "??"
	}
	return "", false
}
