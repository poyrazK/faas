package reposcan_test

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

func nodeService(name string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(`{"name":"` + name + `","scripts":{"start":"node index.js"}}`)}
}

func workloadsByName(t *testing.T, result reposcan.Result) map[string]reposcan.Workload {
	t.Helper()
	out := make(map[string]reposcan.Workload, len(result.Workloads))
	for _, w := range result.Workloads {
		out[w.Name] = w
	}
	return out
}

// ADR-20261025093331: a repository without Compose declares its services'
// policy, visibility and dependencies in .gregale/project.yaml.
func TestProjectConfigConfiguresServicesWithoutCompose(t *testing.T) {
	fsys := fstest.MapFS{
		"services/web/package.json":     nodeService("web"),
		"services/api/package.json":     nodeService("api"),
		"services/billing/package.json": nodeService("billing"),
		"services/ledger/package.json":  nodeService("ledger"),
		".gregale/project.yaml": {Data: []byte(`service_policy: declared
services:
  api:
    service_policy: project
  billing:
    visibility: internal
    depends_on: [ledger]
`)},
	}
	result, err := reposcan.Scan(fsys)
	if err != nil {
		t.Fatal(err)
	}
	got := workloadsByName(t, result)
	if got["web"].ServiceBindingPolicy != reposcan.ServiceBindingPolicyDeclared || got["api"].ServiceBindingPolicy != reposcan.ServiceBindingPolicyProject {
		t.Fatalf("policies: web %q, api %q", got["web"].ServiceBindingPolicy, got["api"].ServiceBindingPolicy)
	}
	if !got["billing"].Unpublished || reposcan.NewWorkloadVisibility(got["billing"]) != api.AppVisibilityInternal || got["web"].Unpublished {
		t.Fatalf("visibility: billing %v, web %v", got["billing"].Unpublished, got["web"].Unpublished)
	}
	if !slices.Equal(got["billing"].DependsOn, []string{"ledger"}) {
		t.Fatalf("billing depends_on = %v", got["billing"].DependsOn)
	}
	if reasons := reposcan.DependencyValidationReasons(result.Workloads, result.Managed); len(reasons) != 0 {
		t.Fatalf("a valid declared graph was rejected: %v", reasons)
	}
}

// Compose stays the more specific source: its declared policy wins and its
// depends_on is merged with the file's.
func TestProjectConfigDefersToCompose(t *testing.T) {
	fsys := fstest.MapFS{
		"compose.yaml": {Data: []byte(`services:
  api:
    image: ghcr.io/example/api:v1
    ports: ["8080:8080"]
    x-gregale-service-policy: declared
    depends_on: [cache]
  cache:
    image: ghcr.io/example/cache:v1
  worker:
    image: ghcr.io/example/worker:v1
`)},
		".gregale/project.yaml": {Data: []byte(`service_policy: project
services:
  api:
    depends_on: [worker, cache]
  worker:
    visibility: public
`)},
	}
	result, err := reposcan.Scan(fsys)
	if err != nil {
		t.Fatal(err)
	}
	got := workloadsByName(t, result)
	if got["api"].ServiceBindingPolicy != reposcan.ServiceBindingPolicyDeclared {
		t.Fatalf("Compose policy lost: %q", got["api"].ServiceBindingPolicy)
	}
	if got["cache"].ServiceBindingPolicy != reposcan.ServiceBindingPolicyProject {
		t.Fatalf("file policy not applied: %q", got["cache"].ServiceBindingPolicy)
	}
	if !slices.Equal(got["api"].DependsOn, []string{"cache", "worker"}) {
		t.Fatalf("depends_on = %v, want Compose's then the file's new names", got["api"].DependsOn)
	}
	if got["worker"].Unpublished || !got["cache"].Unpublished {
		t.Fatalf("visibility: worker %v (file says public), cache %v (no ports)", got["worker"].Unpublished, got["cache"].Unpublished)
	}
}

func TestProjectConfigRejectsInvalidDeclarations(t *testing.T) {
	for _, tc := range []struct {
		name, config, want string
	}{
		{"unknown service", "services:\n  payments:\n    visibility: internal\n", `service "payments" is not in the project; the scan found api, web`},
		{"unknown key", "services:\n  api:\n    visiblity: internal\n", "field visiblity not found"},
		{"bad visibility", "services:\n  api:\n    visibility: private\n", "visibility must be public or internal"},
		{"bad policy", "service_policy: everyone\n", "service_policy must be project, declared or account"},
		{"empty dependency", "services:\n  api:\n    depends_on: ['']\n", "empty depends_on entry"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := reposcan.Scan(fstest.MapFS{
				"services/web/package.json": nodeService("web"),
				"services/api/package.json": nodeService("api"),
				".gregale/project.yaml":     {Data: []byte(tc.config)},
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), ".gregale/project.yaml") {
				t.Fatalf("err = %v, want it to name the file and %q", err, tc.want)
			}
		})
	}
	// A dependency on a service the scan did not find is rejected by the
	// plan's existing graph gate.
	result, err := reposcan.Scan(fstest.MapFS{
		"services/api/package.json": nodeService("api"),
		".gregale/project.yaml":     {Data: []byte("services:\n  api:\n    depends_on: [ghost]\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reasons := reposcan.DependencyValidationReasons(result.Workloads, result.Managed); len(reasons) == 0 {
		t.Fatal("a dependency on a missing service passed the plan gate")
	}
}

func TestProjectConfigAbsentChangesNothing(t *testing.T) {
	result, err := reposcan.Scan(fstest.MapFS{"services/api/package.json": nodeService("api"), "services/web/package.json": nodeService("web")})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range result.Workloads {
		if w.ServiceBindingPolicy != "" || w.Unpublished || len(w.DependsOn) != 0 {
			t.Fatalf("%s changed without a project file: %+v", w.Name, w)
		}
	}
}
