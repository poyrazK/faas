package reconcile

import (
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceEnvForWorkload(t *testing.T) {
	got := serviceEnvForWorkloadWithAvailable(
		map[string]string{"APP_MODE": "prod", "GREGALE_SERVICE_OLD_URL": "stale"},
		reposcan.Workload{Name: "api", DependsOn: []string{"db", "cache"}},
		map[string]struct{}{"db": {}, "cache": {}},
	)
	want := map[string]string{
		"APP_MODE":                  "prod",
		"GREGALE_SERVICE_DB_URL":    "http://db.svc.gregale:10080",
		"GREGALE_SERVICE_CACHE_URL": "http://cache.svc.gregale:10080",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("service env = %#v, want %#v", got, want)
	}
}

func TestServiceEnvSkipsManagedDependencies(t *testing.T) {
	got := serviceEnvForWorkloadWithAvailable(nil,
		reposcan.Workload{Name: "api", DependsOn: []string{"db"}},
		map[string]struct{}{},
	)
	if got != nil {
		t.Fatalf("service env = %#v, want nil", got)
	}
}

func TestServiceBindingsForWorkloadAreStableAndDeduplicated(t *testing.T) {
	got := serviceBindingsForWorkloadWithAvailable(
		reposcan.Workload{Name: "api", DependsOn: []string{" DB ", "cache", "db", "managed"}},
		map[string]struct{}{"db": {}, "cache": {}},
	)
	want := []api.AppServiceBinding{
		{Binding: "GREGALE_SERVICE_CACHE_URL", Service: "cache"},
		{Binding: "GREGALE_SERVICE_DB_URL", Service: "db"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("service bindings = %#v, want %#v", got, want)
	}
}

func TestDiffFieldsChangedBackfillsServiceBindingReadModel(t *testing.T) {
	workload := reposcan.Workload{Name: "api", DependsOn: []string{"db"}}
	app := state.App{
		WorkloadName:  "api",
		WorkloadClass: state.WorkloadClassHTTP,
		Manifest: state.AppManifest{Env: map[string]string{
			"GREGALE_SERVICE_DB_URL": "http://db.svc.gregale:10080",
		}},
	}
	got := diffFieldsChanged(app, workload, "", map[string]struct{}{"api": {}, "db": {}})
	want := []string{"service_bindings"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changed fields = %v, want %v", got, want)
	}
}
