package main

import (
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/reposcan"
)

func TestScopeGenericRootWorkloadUsesScannedProjectIdentity(t *testing.T) {
	for name, source := range map[string]fstest.MapFS{
		"root floor": {
			"package.json": &fstest.MapFile{Data: []byte(`{"scripts":{"start":"node index.js"}}`)},
		},
		"generic Procfile": {
			"Procfile": &fstest.MapFile{Data: []byte("web: gunicorn app:app\n")},
		},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := reposcan.Scan(source)
			if err != nil {
				t.Fatal(err)
			}
			scopeGenericRootWorkload(&result, "template-project")
			if len(result.Workloads) != 1 || result.Workloads[0].Name != "template-project" {
				t.Fatalf("scoped workloads = %#v", result.Workloads)
			}
		})
	}
}

func TestScopeGenericRootWorkloadUsesProjectIdentity(t *testing.T) {
	tests := []struct {
		name      string
		workloads []reposcan.Workload
		wantNames []string
	}{
		{
			name:      "root floor",
			workloads: []reposcan.Workload{{Name: "app", Source: "root-floor"}},
			wantNames: []string{"hello-node"},
		},
		{
			name: "generic Procfile",
			workloads: []reposcan.Workload{{
				Name: "web", Source: "Procfile: web",
				DetectedBy: reposcan.Detection{Detector: "procfile"},
			}},
			wantNames: []string{"hello-node"},
		},
		{
			name: "explicit service",
			workloads: []reposcan.Workload{{
				Name: "app", Source: "compose.yaml: app",
				DetectedBy: reposcan.Detection{Detector: "compose"},
			}},
			wantNames: []string{"app"},
		},
		{
			name: "multi workload",
			workloads: []reposcan.Workload{
				{Name: "app", Source: "root-floor"},
				{Name: "worker", RootDir: "worker", Source: "package.json"},
			},
			wantNames: []string{"app", "worker"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := reposcan.Result{Workloads: append([]reposcan.Workload(nil), test.workloads...)}
			scopeGenericRootWorkload(&result, "hello-node")
			if len(result.Workloads) != len(test.wantNames) {
				t.Fatalf("workload count = %d, want %d", len(result.Workloads), len(test.wantNames))
			}
			for i, want := range test.wantNames {
				if result.Workloads[i].Name != want {
					t.Errorf("workload[%d].Name = %q, want %q", i, result.Workloads[i].Name, want)
				}
			}
		})
	}
}
