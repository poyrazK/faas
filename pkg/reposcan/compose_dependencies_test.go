// adr: 646
package reposcan

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestComposeDependencyConditionsPreserved(t *testing.T) {
	fsys := fstest.MapFS{"compose.yaml": {Data: []byte(`services:
  web:
    image: nginx:1.27
    ports: ["8080:80"]
    depends_on:
      api:
        condition: service_healthy
        required: true
  api:
    image: nginx:1.27
    expose: ["80"]
`)}}
	scan, err := Scan(fsys)
	if err != nil {
		t.Fatal(err)
	}
	for _, workload := range scan.Workloads {
		if workload.Name == "web" {
			if !reflect.DeepEqual(workload.DependsOnConditions, map[string]string{"api": api.ComposeDependencyHealthy}) {
				t.Fatalf("lost condition: %+v", workload)
			}
			if reasons := DependencyValidationReasons(scan.Workloads, scan.Managed); len(reasons) != 0 {
				t.Fatal(reasons)
			}
			return
		}
	}
	t.Fatal("web workload missing")
}

func TestComposeDependencyConditionsRejectUnsupportedDeclarations(t *testing.T) {
	for _, tc := range []struct{ name, declaration, want string }{
		{"completion", "condition: service_completed_successfully", "completion gates are unsupported"},
		{"unknown", "condition: ready", "dependency condition"},
		{"optional health", "condition: service_healthy\n        required: false", "required: true"},
		{"wrong type", "condition: 123", "must be a string"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Scan(fstest.MapFS{"compose.yaml": {Data: []byte("services:\n  web:\n    image: nginx:1.27\n    depends_on:\n      api:\n        " + tc.declaration + "\n  api:\n    image: nginx:1.27\n")}})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestComposeHealthyManagedDependencyRejected(t *testing.T) {
	workloads := []Workload{{Name: "web", DependsOn: []string{"db"}, DependsOnConditions: map[string]string{"db": api.ComposeDependencyHealthy}}}
	reasons := DependencyValidationReasons(workloads, []Managed{{Name: "db", Kind: "postgres"}})
	if len(reasons) != 1 || !strings.Contains(reasons[0], "managed service") {
		t.Fatalf("managed health gate silently accepted: %v", reasons)
	}
	workloads[0].DependsOnConditions["db"] = api.ComposeDependencyStarted
	if reasons := DependencyValidationReasons(workloads, []Managed{{Name: "db"}}); len(reasons) != 0 {
		t.Fatalf("external ordering regressed: %v", reasons)
	}
}
