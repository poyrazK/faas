package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

// multiServiceSource explains why srcDir is a multi-service project rather
// than one app, or returns "" when it is one app. A root with no app shape
// is a project when it holds a Compose file or service roots below it. A
// root whose markers name no runnable app (a package.json that only lists
// workspaces, a Go module with no main package at its root) is a project
// when the scanner finds two or more services below it.
func multiServiceSource(srcDir string) string {
	if detectShape(srcDir) == shapeUnknown {
		if compose := composeFileIn(srcDir); compose != "" {
			return compose + " defines a Compose project"
		}
		if detectNestedMarkerHint(srcDir) {
			return filepath.Base(srcDir) + " holds several services"
		}
		return ""
	}
	if rootIsRunnableApp(srcDir) {
		return ""
	}
	if n := nestedServiceCount(srcDir); n >= 2 {
		return fmt.Sprintf("%s holds %d services and no app at its root", filepath.Base(srcDir), n)
	}
	return ""
}

// rootIsRunnableApp reports whether srcDir itself starts as one app: a
// function, a Dockerfile, a Go main package at the root, or a framework
// profile with an inferred start command.
func rootIsRunnableApp(srcDir string) bool {
	if detectShape(srcDir) == shapeFunction {
		return true
	}
	switch detectFramework(srcDir) {
	case fwDocker:
		return true
	case fwGo:
		// The Go profile always infers `go run .`, so look for the package.
		return goMainPackageAt(srcDir)
	}
	profile, err := frameworkprofile.AnalyzeDir(srcDir)
	return err == nil && profile.Inferred
}

// goMainPackageAt reports whether a non-test .go file directly in dir
// declares package main.
func goMainPackageAt(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := openCustomerFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, "package ") {
				if strings.Fields(line)[1] == "main" {
					_ = file.Close()
					return true
				}
				break
			}
		}
		_ = file.Close()
	}
	return false
}

// nestedServiceCount is how many services the project scanner finds below
// root: workspace, convention and Compose workloads and the commands of a Go
// module (ADR-20261025134512), not the root itself.
func nestedServiceCount(root string) int {
	scan, err := reposcan.Scan(os.DirFS(root))
	if err != nil {
		return 0
	}
	n := 0
	for _, workload := range scan.Workloads {
		if (workload.RootDir != "" && workload.RootDir != ".") || workload.Tier == reposcan.TierCompose || workload.GoCommand != "" {
			n++
		}
	}
	return n
}
