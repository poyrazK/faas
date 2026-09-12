package reposcan

import (
	"reflect"
	"strings"
	"testing"
)

func TestDependencyOrder(t *testing.T) {
	workloads := []Workload{
		{Name: "api", DependsOn: []string{"worker", "db"}},
		{Name: "worker", DependsOn: []string{"db"}},
		{Name: "db"},
	}
	got, err := DependencyOrder(workloads, nil)
	if err != nil {
		t.Fatalf("DependencyOrder: %v", err)
	}
	want := []string{"db", "worker", "api"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DependencyOrder = %v, want %v", got, want)
	}
}

func TestDependencyValidationAllowsManagedAndRejectsCycle(t *testing.T) {
	workloads := []Workload{
		{Name: "api", DependsOn: []string{"db", "worker"}},
		{Name: "worker", DependsOn: []string{"api"}},
	}
	reasons := DependencyValidationReasons(workloads, []Managed{{Name: "db"}})
	if len(reasons) != 1 || !strings.Contains(reasons[0], "cycle") {
		t.Fatalf("DependencyValidationReasons = %v, want cycle only", reasons)
	}
}

func TestDependencyNamesShortAndLongForms(t *testing.T) {
	short := dependencyNames([]any{"api", "db", "api"})
	long := dependencyNames(map[string]any{"db": map[string]any{"condition": "service_healthy"}, "api": nil})
	if !reflect.DeepEqual(short, []string{"api", "db"}) {
		t.Fatalf("short dependencies = %v", short)
	}
	if !reflect.DeepEqual(long, []string{"api", "db"}) {
		t.Fatalf("long dependencies = %v", long)
	}
}
