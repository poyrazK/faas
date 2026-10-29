package reposcan

import (
	"bufio"
	"io/fs"
	"path"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// detectGoModuleCommands emits one workload per `cmd/<name>` main package of
// a single Go module at the repository root (ADR-20261025134512). Each builds
// from the module root, so shared internal packages stay in its context, and
// names its command in GoCommand. It applies only to a module with no main
// package at its root and at least two commands: Railpack builds a root main
// package or the first command by itself, so a root app or a lone command
// keeps the one-app layout (and the name) it already deploys with. A root
// Dockerfile decides the build itself, and a command directory with its own
// Dockerfile or go.mod is a convention member already.
func detectGoModuleCommands(fsys fs.FS) []workloadSeed {
	if !isRegularFile(fsys, "go.mod") || isRegularFile(fsys, "go.work") || goMainPackageIn(fsys, ".") ||
		isRegularFile(fsys, nameDockerfile) || isRegularFile(fsys, nameDockerfileLower) {
		return nil
	}
	entries, err := fs.ReadDir(fsys, "cmd")
	if err != nil {
		return nil
	}
	var seeds []workloadSeed
	for _, entry := range entries {
		name := entry.Name()
		dir := path.Join("cmd", name)
		if !entry.IsDir() || !api.ValidGoCommand(name) || !goMainPackageIn(fsys, dir) {
			continue
		}
		if _, member, _ := runnableWorkspaceSeed(fsys, dir, "convention", false); member {
			continue // its own Dockerfile or module: a convention member already
		}
		seeds = append(seeds, workloadSeed{
			name:      name,
			rootDir:   ".",
			goCommand: name,
			source:    "convention: " + dir + " (Go module root)",
		})
	}
	if len(seeds) < 2 {
		return nil
	}
	return seeds
}

// goMainPackageIn reports whether a non-test .go file directly in dir
// declares package main.
func goMainPackageIn(fsys fs.FS, dir string) bool {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if goFilePackage(fsys, path.Join(dir, name)) == "main" {
			return true
		}
	}
	return false
}

// goFilePackage returns the package clause of a Go file, or "" when none is
// found before the first long line or read error.
func goFilePackage(fsys fs.FS, name string) string {
	file, err := fsys.Open(name)
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if rest, ok := strings.CutPrefix(line, "package "); ok {
			if fields := strings.Fields(rest); len(fields) > 0 {
				return fields[0]
			}
			return ""
		}
	}
	return ""
}

// dropShadowedGoCommands removes a Go command workload whose name another
// source already declares, such as a Compose service building that command
// with its own Dockerfile: the declared workload wins, and a project never
// gets two services of one name.
func dropShadowedGoCommands(seeds []workloadSeed) []workloadSeed {
	declared := map[string]bool{}
	for _, s := range seeds {
		if s.goCommand == "" {
			declared[strings.ToLower(s.name)] = true
		}
	}
	out := seeds[:0]
	for _, s := range seeds {
		if s.goCommand == "" || !declared[strings.ToLower(s.name)] {
			out = append(out, s)
		}
	}
	return out
}

func isRegularFile(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && info.Mode().IsRegular()
}

// goCommandForBuild keeps a Go command only for a Railpack source build: a
// Dockerfile or a prebuilt image decides its own binary.
func goCommandForBuild(command, dockerfile, image string) string {
	if dockerfile != "" || image != "" {
		return ""
	}
	return command
}
