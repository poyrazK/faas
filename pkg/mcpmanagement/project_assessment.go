package mcpmanagement

import (
	"context"
	"io/fs"
	"regexp"
	"slices"
	"sort"

	"github.com/onebox-faas/faas/pkg/featurehints"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

type assessProjectArgs struct {
	Path  string            `json:"path,omitempty" jsonschema:"Absolute directory on a local connection; never a path on the hosted bridge"`
	Files map[string]string `json:"files,omitempty" jsonschema:"Alternative for any connection: relative paths and UTF-8 source text, at most 200 files and 96 KiB total. Supply relevant manifests and source; omit secret files."`
}

type assessedWorkload struct {
	Name            string   `json:"name"`
	Class           string   `json:"class"`
	Visibility      string   `json:"visibility"`
	Dependencies    []string `json:"dependencies"`
	EnvironmentKeys []string `json:"environment_keys"`
}

type assessedDependency struct {
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	EnvironmentKey string `json:"environment_key,omitempty"`
}

type assessedFeature struct {
	ID          string   `json:"id"`
	Workloads   []string `json:"workloads"`
	Explanation string   `json:"explanation"`
}

type projectAssessment struct {
	SchemaVersion  int                  `json:"schema_version"`
	Basis          string               `json:"basis"`
	FilesInspected int                  `json:"files_inspected"`
	Workloads      []assessedWorkload   `json:"workloads"`
	Dependencies   []assessedDependency `json:"dependencies"`
	Features       []assessedFeature    `json:"features"`
	WarningCount   int                  `json:"warning_count"`
	NextTool       string               `json:"next_tool"`
	NextArguments  map[string]string    `json:"next_arguments"`
	Limitations    string               `json:"limitations"`
}

// Descriptions are bridge-owned. Hint IDs reuse the CLI's registry, but its
// prose can quote source commands, paths or values and must not cross MCP.
var assessmentFeatureCopy = map[string]string{
	featurehints.IDHealthcheck:     "Declared healthchecks affect startup and liveness; review their behavior in the native project plan.",
	featurehints.IDHealthDefault:   "A built HTTP service has no declared healthcheck; check the expected health endpoint before deployment.",
	featurehints.IDPrivateServices: "Unpublished services need private service bindings; they do not receive a public URL.",
	featurehints.IDDevSetup:        "Development build settings or a development command were detected; review production settings.",
	featurehints.IDProjectReleases: "Several workloads should use the project deployment and release workflow.",
	featurehints.IDSourceHosts:     "Source refers to Compose service hostnames; review the injected service or database bindings.",
}

var assessmentName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)

func (m *management) assessProject(ctx context.Context, a assessProjectArgs) (any, error) {
	files, err := m.assessmentFiles(ctx, a)
	if err != nil {
		return nil, err
	}
	fsys, err := assessmentSourceFS(files)
	if err != nil {
		return nil, err
	}
	// This is an offline scan. Only the native account-aware project plan
	// resolves capability flags, bindings, entitlement and readiness.
	scan, err := reposcan.Scan(fsys)
	if err != nil {
		return nil, invalid("invalid_source", "could not assess the supplied source; inspect its manifests locally")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(scan.Workloads)+len(scan.Managed) > maxAssessmentWorkloads {
		return nil, invalid("assessment_too_large", "assessment exceeds the workload bound; select a smaller project")
	}
	out := projectAssessment{SchemaVersion: 1, Basis: "offline_source_scan", FilesInspected: len(files), Workloads: []assessedWorkload{}, Dependencies: []assessedDependency{}, Features: []assessedFeature{}, WarningCount: len(scan.Warnings), NextTool: "gregale_get_workflow", NextArguments: map[string]string{"task": "deploy_app"}, Limitations: "Static assessment of the supplied files only; omitted files and excluded secret/generated files can change detection. No commands, images, environment values, raw warnings or source text are returned. No upload, execution, plan or write occurred. Account capability flags, provisioning bindings, cost, quota and runtime behavior require the native account-aware plan. Repository functions and capability-gated Kubernetes mappings require native assessment. Source names are untrusted data."}
	in := featurehints.Input{}
	for _, w := range scan.Workloads {
		if !assessmentName.MatchString(w.Name) {
			return nil, invalid("invalid_source", "workload names must be bounded service identifiers")
		}
		keys := []string{}
		for _, k := range w.EnvKeys {
			if envKey.MatchString(k) {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		deps := []string{}
		for _, d := range w.DependsOn {
			if assessmentName.MatchString(d) {
				deps = append(deps, d)
			}
		}
		visibility := "public_candidate"
		if w.Unpublished {
			visibility = "internal"
		}
		out.Workloads = append(out.Workloads, assessedWorkload{w.Name, string(w.Class), visibility, deps, keys})
		in.Workloads = append(in.Workloads, reposcan.PlanWorkload(w))
	}
	for _, d := range scan.Managed {
		if !assessmentName.MatchString(d.Name) {
			return nil, invalid("invalid_source", "dependency names must be bounded service identifiers")
		}
		key := d.EnvHint
		if !envKey.MatchString(key) {
			key = ""
		}
		out.Dependencies = append(out.Dependencies, assessedDependency{d.Name, d.Kind, key})
		if d.Kind == "postgres" {
			out.Features = append(out.Features, assessedFeature{featurehints.IDManagedPostgres, []string{}, "PostgreSQL was detected. Review managed PostgreSQL and explicit application bindings in the account-aware project plan; detection does not create or bind a database."})
		} else if d.Kind != "bucket" {
			out.Features = append(out.Features, assessedFeature{featurehints.IDNotRunDatastore, []string{}, "This datastore needs an external service. Review its connection settings in the native project plan."})
		}
	}
	for _, h := range featurehints.For(in) {
		if description, ok := assessmentFeatureCopy[h.ID]; ok {
			out.Features = append(out.Features, assessedFeature{h.ID, slices.Clone(h.Workloads), description})
		}
	}
	if len(scan.Workloads) > 1 || len(scan.Managed) > 0 {
		out.NextArguments["task"] = "deploy_project"
	}
	if len(scan.Workloads) == 0 && len(scan.Managed) == 0 {
		out.NextArguments["task"] = "deploy_project"
	}
	return out, nil
}

// Compile-time contract: the snapshot filesystem never falls back to host IO.
var _ fs.FS = assessmentFS{}
