package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

// scenarioInternalServices names the scenario services that the project
// declares internal (.gregale/project.yaml `visibility: internal`, or a
// Compose service without ports). Their ephemeral test apps keep internal
// visibility instead of becoming public URLs (ChronoLake finding 16). A
// service matches a project workload by name or by source directory. The
// scan is best effort: a repository it cannot read declares nothing.
func scenarioInternalServices(manifestDir string, scenario testScenario) map[string]bool {
	internal := map[string]bool{}
	if len(scenario.Services) == 0 {
		return internal
	}
	root, err := gitRootFromCwd(manifestDir)
	if err != nil {
		root = manifestDir
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return internal
	}
	scan, err := reposcan.Scan(os.DirFS(root))
	if err != nil {
		return internal
	}
	byName := map[string]bool{}
	byDir := map[string]bool{}
	for _, workload := range scan.Workloads {
		if !workload.Unpublished {
			continue
		}
		byName[strings.ToLower(workload.Name)] = true
		byDir[filepath.Clean(filepath.FromSlash(workload.RootDir))] = true
	}
	for name, spec := range scenario.Services {
		if spec.Fixture != "" {
			continue
		}
		if byName[strings.ToLower(name)] {
			internal[name] = true
			continue
		}
		dir, err := resolveDeploySourceDir(manifestDir, spec.Source)
		if err != nil {
			continue
		}
		if abs, err := filepath.Abs(dir); err == nil {
			if rel, err := filepath.Rel(root, abs); err == nil && filepath.IsLocal(rel) && byDir[filepath.Clean(rel)] {
				internal[name] = true
			}
		}
	}
	return internal
}

// configureTestWorkloadVisibility makes a test app internal before its first
// deployment, so it is never reachable from the public edge.
func configureTestWorkloadVisibility(ctx context.Context, client testAccessClient, slug string, internal bool) error {
	if !internal {
		return nil
	}
	visibility := string(api.AppVisibilityInternal)
	_, err := client.UpdateApp(ctx, slug, api.UpdateAppRequest{Visibility: &visibility})
	return err
}
