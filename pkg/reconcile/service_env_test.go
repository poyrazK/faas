package reconcile

import (
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/reposcan"
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
