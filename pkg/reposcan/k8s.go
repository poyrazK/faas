package reposcan

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// k8sManifest is the minimal subset of a k8s YAML resource we read.
// apiVersion + kind + metadata.name are the routing decisions;
// spec.schedule is for CronJob only; spec.template.spec.containers
// carries command/env/ports for Deployment (the stateless workload
// we actually provision).
type k8sManifest struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		// CronJob-only
		Schedule string `yaml:"schedule"`
		// Deployment / StatefulSet
		Template struct {
			Spec struct {
				Containers []k8sContainer `yaml:"containers"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
}
type k8sContainer struct {
	Name    string   `yaml:"name"`
	Command []string `yaml:"command"`
	Args    []string `yaml:"args"`
	Image   string   `yaml:"image"`
	Env     []k8sEnv `yaml:"env"`
	Ports   []struct {
		ContainerPort int `yaml:"containerPort"`
	} `yaml:"ports"`
}
type k8sEnv struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// k8sRootDirs is the set of directories at repo root that may
// contain k8s manifests (impl plan §3). Order matters; the first
// present directory wins per file walk.
var k8sRootDirs = []string{nameK8s, nameKubernetes, nameDeploy, nameManifests}

// k8sManifestExts is the set of file extensions we recognize as
// k8s manifests inside a k8s/ subdir. We accept .yaml, .yml, and
// unmanifest JSON is out of scope.
var k8sManifestExts = []string{".yaml", ".yml"}

// detectK8s walks each present k8s subdirectory and decodes every
// YAML file inside. Multi-document YAML stream markers (including
// comments and trailing whitespace) delimit independent documents.
// StatefulSet is refused (ADR-046 — the stateless contract
// covers K8s, not just compose). Deployment → http class hint
// (only stateless pods run on the platform). CronJob → job + the
// declared schedule.
//
// Image refs in k8s containers can be denylisted datastores — we
// surface them as Managed (compose already does this; the customer
// should see both rows in one confirm table).
func detectK8s(fsys fs.FS) ([]workloadSeed, []Managed, []string, error) {
	var (
		seeds    []workloadSeed
		managed  []Managed
		warnings []string
	)
	for _, d := range k8sRootDirs {
		// Stat the directory; if present, walk it.
		info, err := fs.Stat(fsys, d)
		if err != nil || !info.IsDir() {
			continue
		}
		err = fs.WalkDir(fsys, d, func(p string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			ext := strings.ToLower(path.Ext(p))
			if !containsExt(k8sManifestExts, ext) {
				return nil
			}
			body, err := readValidFile(fsys, p)
			if err != nil {
				return err
			}
			docs := splitYAMLDocs(body)
			for docIndex, doc := range docs {
				if len(strings.TrimSpace(doc)) == 0 {
					continue
				}
				var m k8sManifest
				if err := yaml.Unmarshal([]byte(doc), &m); err != nil {
					warnings = append(warnings, fmt.Sprintf(
						"reposcan: parse %s document %d: %v", p, docIndex+1, err))
					continue
				}
				switch m.Kind {
				case "Deployment":
					// Stateless → http. The first container's
					// command/args/env/ports/apply. (We don't
					// model initContainers / sidecars here.)
					seeds = append(seeds, k8sDeploymentSeed(p, m))
				case "CronJob":
					seeds = append(seeds, k8sCronJobSeed(p, m))
				case "StatefulSet":
					warnings = append(warnings, "reposcan: "+p+
						": refusing StatefulSet "+m.Metadata.Name+
						" (stateless contract, ADR-046)")
				default:
					// Service / ConfigMap / Secret / Ingress /
					// unknown: skip silently. They're plumbing,
					// not workloads.
				}
			}
			return nil
		})
		if err != nil {
			return nil, nil, nil, err
		}
	}
	// Deterministic order to keep the merge input stable.
	sort.SliceStable(seeds, func(i, j int) bool { return seeds[i].name < seeds[j].name })
	return seeds, managed, warnings, nil
}

func containsExt(list []string, ext string) bool {
	for _, e := range list {
		if e == ext {
			return true
		}
	}
	return false
}

func splitYAMLDocs(body []byte) []string {
	s := strings.ReplaceAll(string(body), "\r\n", "\n")
	// YAML document markers must start at column zero. The marker may
	// carry trailing whitespace or a comment (for example, `--- # worker`).
	// Explicit `...` end markers also terminate the current document.
	var out []string
	var cur strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if isYAMLDocMarker(line, "---") || isYAMLDocMarker(line, "...") {
			if cur.Len() > 0 || len(out) > 0 {
				out = append(out, cur.String())
			}
			cur.Reset()
			continue
		}
		cur.WriteString(line)
		cur.WriteByte('\n')
	}
	if cur.Len() > 0 || len(out) == 0 {
		out = append(out, cur.String())
	}
	return out
}

func isYAMLDocMarker(line, marker string) bool {
	if !strings.HasPrefix(line, marker) {
		return false
	}
	rest := strings.TrimSpace(line[len(marker):])
	return rest == "" || strings.HasPrefix(rest, "#")
}

func k8sDeploymentSeed(src string, m k8sManifest) workloadSeed {
	s := workloadSeed{
		name:   m.Metadata.Name,
		source: src + ": " + m.Metadata.Name,
		class:  ClassHTTP,
	}
	if len(m.Spec.Template.Spec.Containers) > 0 {
		c := m.Spec.Template.Spec.Containers[0]
		if len(c.Command) > 0 {
			s.command = append(s.command, c.Command...)
		}
		if len(c.Args) > 0 {
			s.command = append(s.command, c.Args...)
		}
		for _, e := range c.Env {
			s.envKeys = append(s.envKeys, e.Name)
		}
		for _, p := range c.Ports {
			if p.ContainerPort != 0 {
				s.ports = append(s.ports, p.ContainerPort)
			}
		}
		sort.Ints(s.ports)
		sort.Strings(s.envKeys)
	}
	return s
}

func k8sCronJobSeed(src string, m k8sManifest) workloadSeed {
	return workloadSeed{
		name:     m.Metadata.Name,
		source:   src + ": " + m.Metadata.Name,
		class:    ClassJob,
		schedule: m.Spec.Schedule,
	}
}
