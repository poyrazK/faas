package projectsetup

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/reposcan"
)

func TestManagedConsumers(t *testing.T) {
	value := func(s string) *string { return &s }
	env := reposcan.ComposeEnv{Services: map[string]map[string]*string{
		"api":    {"DATABASE_URL": value("postgres://app:secret@db:5432/app"), "READ_URL": value("postgres://reader@db/app")},
		"worker": {},
		"report": {"DATABASE_URL": value("postgres://u@elsewhere.example.com/app")},
		"web":    {"API_URL": value("http://api:8080")},
	}}
	workloads := []reposcan.Workload{
		{Name: "api", DependsOn: []string{"db"}},
		{Name: "worker", DependsOn: []string{"db"}},
		{Name: "report", DependsOn: []string{"db"}},
		{Name: "web", DependsOn: []string{"api"}},
	}
	got := ManagedConsumers(env, workloads, "db", "DATABASE_URL")
	want := []ManagedConsumer{
		{Workload: "api", EnvKey: "DATABASE_URL"}, {Workload: "api", EnvKey: "READ_URL"},
		{Workload: "worker", EnvKey: "DATABASE_URL"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("consumers = %+v, want %+v (report keeps its own database, web does not use db)", got, want)
	}
	if got := ManagedConsumers(env, workloads, "cache", "REDIS_URL"); len(got) != 0 {
		t.Fatalf("unused datastore consumers = %+v", got)
	}
}
