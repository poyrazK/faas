package reposcan

import (
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/projectconfig"
)

// ProjectConfigPath is the deprecated standalone project declaration for what
// Compose declares about its own services (ADR-20261025093331). The root
// gregale.yaml's `project:` block replaces it (ADR-20261107180000).
const ProjectConfigPath = projectconfig.LegacyPath

// projectConfigMigrationHint tells a repository with the legacy file how to
// move to the one source.
const projectConfigMigrationHint = "run `gregale project import` to move it into the root gregale.yaml `project:` block"

// loadProjectConfig returns the repository's project settings and where they
// came from. The root gregale.yaml `project:` block is the source; the legacy
// .gregale/project.yaml is still read, with a deprecation warning. Declaring
// both is refused: two files that can disagree is what the one-model rule
// removes.
func loadProjectConfig(fsys fs.FS) (cfg projectconfig.Config, source string, warnings []string, err error) {
	manifestCfg, manifestSource, err := manifestProjectConfig(fsys)
	if err != nil {
		return projectconfig.Config{}, "", nil, err
	}
	body, err := readValidFile(fsys, ProjectConfigPath)
	if errors.Is(err, fs.ErrNotExist) {
		return manifestCfg, manifestSource, nil, nil
	}
	if err != nil {
		return projectconfig.Config{}, "", nil, fmt.Errorf("reposcan: read %s: %w", ProjectConfigPath, err)
	}
	if manifestSource != "" {
		return projectconfig.Config{}, "", nil, fmt.Errorf("reposcan: project settings are declared in both %s and %s; keep one (%s)", manifestSource, ProjectConfigPath, projectConfigMigrationHint)
	}
	legacy, err := projectconfig.Parse(body)
	if err != nil {
		return projectconfig.Config{}, "", nil, fmt.Errorf("reposcan: parse %s: %w", ProjectConfigPath, err)
	}
	return legacy, ProjectConfigPath, []string{fmt.Sprintf("%s is deprecated; %s", ProjectConfigPath, projectConfigMigrationHint)}, nil
}

// manifestProjectConfig reads the root manifest's project block. source is
// empty when no root manifest declares one.
func manifestProjectConfig(fsys fs.FS) (projectconfig.Config, string, error) {
	for _, name := range projectconfig.ManifestNames {
		body, err := readValidFile(fsys, name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return projectconfig.Config{}, "", fmt.Errorf("reposcan: read %s: %w", name, err)
		}
		cfg, found, err := projectconfig.FromManifest(body)
		if err != nil {
			return projectconfig.Config{}, "", fmt.Errorf("reposcan: parse %s: %w", name, err)
		}
		if !found {
			return projectconfig.Config{}, "", nil
		}
		return cfg, name + " `project:`", nil
	}
	return projectconfig.Config{}, "", nil
}

// applyProjectConfig overlays the project settings on the scanned workloads.
// Compose declarations stay the more specific source: a declared
// x-gregale-service-policy wins and depends_on lists are merged. No settings
// change nothing; invalid ones fail the scan. It returns any deprecation
// warning.
func applyProjectConfig(fsys fs.FS, workloads []Workload) ([]string, error) {
	cfg, source, warnings, err := loadProjectConfig(fsys)
	if err != nil || source == "" {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("reposcan: %s: %w", source, err)
	}
	projectPolicy, err := projectConfigPolicy(cfg.ServicePolicy)
	if err != nil {
		return nil, fmt.Errorf("reposcan: %s: %w", source, err)
	}
	byName := make(map[string]int, len(workloads))
	for i, w := range workloads {
		byName[strings.ToLower(w.Name)] = i
	}
	services := make(map[int]projectconfig.Service, len(cfg.Services))
	for name, service := range cfg.Services {
		i, ok := byName[strings.ToLower(strings.TrimSpace(name))]
		if !ok {
			return nil, fmt.Errorf("reposcan: %s: service %q is not in the project; the scan found %s", source, name, workloadNames(workloads))
		}
		services[i] = service
	}
	for i := range workloads {
		w := &workloads[i]
		service := services[i]
		if w.ServiceBindingPolicy == "" {
			policy, _ := projectConfigPolicy(service.ServicePolicy)
			if policy == "" {
				policy = projectPolicy
			}
			w.ServiceBindingPolicy = policy
		}
		switch service.Visibility {
		case "internal":
			w.Unpublished = true
		case "public":
			w.Unpublished = false
		}
		w.DependsOn = mergeDependsOn(w.DependsOn, service.DependsOn)
	}
	return warnings, nil
}

func projectConfigPolicy(value string) (ServiceBindingPolicy, error) {
	policy, err := normalizeServiceBindingPolicy(value)
	if err != nil {
		return "", fmt.Errorf("service_policy must be project, declared or account, got %q", value)
	}
	return policy, nil
}

// mergeDependsOn appends the names in extra that existing does not already
// hold, compared case-insensitively.
func mergeDependsOn(existing, extra []string) []string {
	seen := make(map[string]bool, len(existing)+len(extra))
	for _, name := range existing {
		seen[strings.ToLower(strings.TrimSpace(name))] = true
	}
	for _, name := range extra {
		name = strings.TrimSpace(name)
		if key := strings.ToLower(name); !seen[key] {
			seen[key] = true
			existing = append(existing, name)
		}
	}
	return existing
}

func workloadNames(workloads []Workload) string {
	names := make([]string, 0, len(workloads))
	for _, w := range workloads {
		names = append(names, w.Name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "no services"
	}
	return strings.Join(names, ", ")
}
