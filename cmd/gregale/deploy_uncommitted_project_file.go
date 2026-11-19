package main

import (
	"fmt"
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

// uncommittedBuildFiles lists, in `git status --porcelain` order, the build
// inputs under the deployed source root that a HEAD deploy leaves behind:
// Dockerfiles, Containerfiles and ignore files at any depth. The generic
// "N local changes excluded" warning hid that an edited Dockerfile was not
// the one being built (retail-store feedback B1, 2026-11-17).
func uncommittedBuildFiles(porcelain, repoRoot, sourceDir string) []string {
	rel, err := filepath.Rel(repoRoot, sourceDir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil
	}
	rel = filepath.ToSlash(filepath.Clean(rel))
	var files []string
	for _, line := range strings.Split(porcelain, "\n") {
		if len(line) < 4 {
			continue
		}
		path := strings.Trim(line[3:], `"`)
		if _, renamed, ok := strings.Cut(path, " -> "); ok {
			path = renamed
		}
		path = filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
		if rel != "." && !strings.HasPrefix(path, rel+"/") {
			continue
		}
		if buildInputFileName(filepath.Base(path)) {
			files = append(files, path)
		}
	}
	return files
}

func buildInputFileName(base string) bool {
	lower := strings.ToLower(base)
	switch {
	case lower == "dockerfile", lower == "containerfile", lower == ".dockerignore", lower == ".gregaleignore":
		return true
	case strings.HasPrefix(lower, "dockerfile."), strings.HasSuffix(lower, ".dockerfile"):
		return true
	}
	return false
}

// dirtyBuildFilesSuffix names up to three uncommitted build inputs for the
// HEAD-deploy warning, or returns "" when there are none.
func dirtyBuildFilesSuffix(files []string) string {
	if len(files) == 0 {
		return ""
	}
	shown := files
	if len(shown) > 3 {
		shown = shown[:3]
	}
	text := strings.Join(shown, ", ")
	if more := len(files) - len(shown); more > 0 {
		text += fmt.Sprintf(" and %d more", more)
	}
	return " Not shipped: " + text + "."
}
