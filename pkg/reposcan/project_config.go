package reposcan

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ProjectConfigPath is the repository's project declaration for what Compose
// declares about its own services (ADR-20261025093331).
const ProjectConfigPath = ".gregale/project.yaml"

type projectConfig struct {
	ServicePolicy string                          `yaml:"service_policy"`
	Services      map[string]projectServiceConfig `yaml:"services"`
}

type projectServiceConfig struct {
	ServicePolicy string   `yaml:"service_policy"`
	Visibility    string   `yaml:"visibility"`
	DependsOn     []string `yaml:"depends_on"`
}

// applyProjectConfig overlays .gregale/project.yaml on the scanned
// workloads. Compose declarations stay the more specific source: a declared
// x-gregale-service-policy wins and depends_on lists are merged. A missing
// file changes nothing; an invalid one fails the scan.
func applyProjectConfig(fsys fs.FS, workloads []Workload) error {
	body, err := readValidFile(fsys, ProjectConfigPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reposcan: read %s: %w", ProjectConfigPath, err)
	}
	var cfg projectConfig
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("reposcan: parse %s: %w", ProjectConfigPath, err)
	}
	projectPolicy, err := projectConfigPolicy(cfg.ServicePolicy)
	if err != nil {
		return fmt.Errorf("reposcan: %s: %w", ProjectConfigPath, err)
	}
	byName := make(map[string]int, len(workloads))
	for i, w := range workloads {
		byName[strings.ToLower(w.Name)] = i
	}
	services := make(map[int]projectServiceConfig, len(cfg.Services))
	for name, service := range cfg.Services {
		i, ok := byName[strings.ToLower(strings.TrimSpace(name))]
		if !ok {
			return fmt.Errorf("reposcan: %s: service %q is not in the project; the scan found %s", ProjectConfigPath, name, workloadNames(workloads))
		}
		if _, err := projectConfigPolicy(service.ServicePolicy); err != nil {
			return fmt.Errorf("reposcan: %s: service %q: %w", ProjectConfigPath, name, err)
		}
		if service.Visibility != "" && service.Visibility != "public" && service.Visibility != "internal" {
			return fmt.Errorf("reposcan: %s: service %q visibility must be public or internal, got %q", ProjectConfigPath, name, service.Visibility)
		}
		for _, dependency := range service.DependsOn {
			if strings.TrimSpace(dependency) == "" {
				return fmt.Errorf("reposcan: %s: service %q has an empty depends_on entry", ProjectConfigPath, name)
			}
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
	return nil
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
