package reposcan_test

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/reposcan"
)

// ADR-20261107180000: the root gregale.yaml `project:` block is the one place
// for project-wide service settings.
func TestProjectBlockInRootManifestConfiguresServices(t *testing.T) {
	fsys := fstest.MapFS{
		"services/web/package.json":     nodeService("web"),
		"services/billing/package.json": nodeService("billing"),
		"services/ledger/package.json":  nodeService("ledger"),
		"gregale.yaml": {Data: []byte(`project:
  service_policy: declared
  services:
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
	if len(got) != 3 {
		t.Fatalf("workloads = %v, want web, billing and ledger only (a project-only root manifest is not a service)", keys(got))
	}
	if got["web"].ServiceBindingPolicy != reposcan.ServiceBindingPolicyDeclared {
		t.Fatalf("web policy = %q, want declared", got["web"].ServiceBindingPolicy)
	}
	if !got["billing"].Unpublished || !slices.Equal(got["billing"].DependsOn, []string{"ledger"}) {
		t.Fatalf("billing = unpublished %v depends_on %v", got["billing"].Unpublished, got["billing"].DependsOn)
	}
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "deprecated") {
			t.Fatalf("unexpected deprecation warning: %q", warning)
		}
	}
}

func TestLegacyProjectFileStillAppliesWithDeprecationWarning(t *testing.T) {
	fsys := fstest.MapFS{
		"services/web/package.json": nodeService("web"),
		"services/api/package.json": nodeService("api"),
		".gregale/project.yaml":     {Data: []byte("services:\n  api:\n    visibility: internal\n")},
	}
	result, err := reposcan.Scan(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if !workloadsByName(t, result)["api"].Unpublished {
		t.Fatal("legacy .gregale/project.yaml no longer applied")
	}
	found := false
	for _, warning := range result.Warnings {
		found = found || (strings.Contains(warning, ".gregale/project.yaml is deprecated") && strings.Contains(warning, "gregale project import"))
	}
	if !found {
		t.Fatalf("warnings = %q, want the deprecation and migrate hint", result.Warnings)
	}
}

func TestProjectSettingsInBothPlacesAreRefused(t *testing.T) {
	fsys := fstest.MapFS{
		"services/web/package.json": nodeService("web"),
		"services/api/package.json": nodeService("api"),
		"gregale.yaml":              {Data: []byte("project:\n  service_policy: declared\n")},
		".gregale/project.yaml":     {Data: []byte("service_policy: project\n")},
	}
	_, err := reposcan.Scan(fsys)
	if err == nil || !strings.Contains(err.Error(), "declared in both") || !strings.Contains(err.Error(), "gregale project import") {
		t.Fatalf("err = %v, want a refusal naming both sources and the migrate command", err)
	}
}

func TestProjectBlockIsValidatedStrictly(t *testing.T) {
	for name, body := range map[string]string{
		"unknown key":     "project:\n  servics: {}\n",
		"bad visibility":  "project:\n  services:\n    api:\n      visibility: private\n",
		"unknown service": "project:\n  services:\n    nope:\n      visibility: internal\n",
	} {
		t.Run(name, func(t *testing.T) {
			fsys := fstest.MapFS{
				"services/web/package.json": nodeService("web"),
				"services/api/package.json": nodeService("api"),
				"gregale.yaml":              {Data: []byte(body)},
			}
			if _, err := reposcan.Scan(fsys); err == nil || !strings.Contains(err.Error(), "gregale.yaml") {
				t.Fatalf("err = %v, want a gregale.yaml project error", err)
			}
		})
	}
}

func keys(m map[string]reposcan.Workload) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// ADR-20261122191423: a service's `uses:` grants reach its workload
// normalized, and an invalid grant fails the scan.
func TestProjectBlockUsesGrants(t *testing.T) {
	fsys := fstest.MapFS{
		"services/web/package.json":    nodeService("web"),
		"services/worker/package.json": nodeService("worker"),
		"gregale.yaml": {Data: []byte(`project:
  services:
    web:
      uses:
        queues:
          - app: Worker
            queue: transforms
        delayed_tasks: [self]
        buckets:
          - bucket: assets
            prefix: rooms/
`)},
	}
	result, err := reposcan.Scan(fsys)
	if err != nil {
		t.Fatal(err)
	}
	got := workloadsByName(t, result)
	uses := got["web"].Uses
	if uses == nil || len(uses.Queues) != 1 || uses.Queues[0].App != "worker" || uses.DelayedTasks[0] != "self" ||
		uses.Buckets[0].App != "self" || uses.Buckets[0].Access != "read_write" {
		t.Fatalf("web uses = %+v", uses)
	}
	if got["worker"].Uses != nil {
		t.Fatalf("worker uses = %+v, want none", got["worker"].Uses)
	}

	fsys["gregale.yaml"] = &fstest.MapFile{Data: []byte("project:\n  services:\n    web:\n      uses:\n        queues:\n          - queue: transforms\n")}
	if _, err := reposcan.Scan(fsys); err == nil || !strings.Contains(err.Error(), "app is required") {
		t.Fatalf("grant without app: %v", err)
	}
	fsys["gregale.yaml"] = &fstest.MapFile{Data: []byte("project:\n  services:\n    web:\n      uses:\n        topics: [x]\n")}
	if _, err := reposcan.Scan(fsys); err == nil {
		t.Fatal("unknown uses key accepted")
	}
}
