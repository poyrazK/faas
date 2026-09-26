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

func TestDiffFieldsChangedDetectsServiceBindingPolicy(t *testing.T) {
	workload := reposcan.Workload{
		Name:                 "api",
		ServiceBindingPolicy: reposcan.ServiceBindingPolicyDeclared,
	}
	app := state.App{
		WorkloadName:  "api",
		WorkloadClass: state.WorkloadClassHTTP,
	}
	got := diffFieldsChanged(app, workload, "")
	want := []string{"service_binding_policy"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changed fields = %v, want %v", got, want)
	}
}

func TestDiffFieldsChangedPreservesLegacyServiceBindingPolicy(t *testing.T) {
	workload := reposcan.Workload{Name: "api"}
	app := state.App{WorkloadName: "api", WorkloadClass: state.WorkloadClassHTTP}
	if got := diffFieldsChanged(app, workload, ""); len(got) != 0 {
		t.Fatalf("legacy reapply changed fields = %v, want none", got)
	}
	if got := serviceBindingPolicyForExistingWorkload(workload, app.Manifest.ServiceBindingPolicy); got != api.ServiceBindingPolicyAccount {
		t.Fatalf("legacy policy = %q, want account", got)
	}
}

func TestDiffFieldsChangedDetectsAllowedServiceCallers(t *testing.T) {
	callers := []string{"frontend"}
	workload := reposcan.Workload{Name: "billing", AllowedServiceCallers: &callers}
	app := state.App{WorkloadName: "billing", WorkloadClass: state.WorkloadClassHTTP}
	got := diffFieldsChanged(app, workload, "")
	if !reflect.DeepEqual(got, []string{"allowed_service_callers"}) {
		t.Fatalf("changed fields = %v, want allowed_service_callers", got)
	}
	// Empty is an explicit deny-all policy, not the legacy account policy.
	empty := []string{}
	workload.AllowedServiceCallers = &empty
	got = diffFieldsChanged(app, workload, "")
	if !reflect.DeepEqual(got, []string{"allowed_service_callers"}) {
		t.Fatalf("empty list changed fields = %v", got)
	}
}

func TestDiffFieldsChangedDetectsPreviewServiceCallsPolicy(t *testing.T) {
	workload := reposcan.Workload{
		Name:                      "billing",
		PreviewServiceCallsPolicy: reposcan.PreviewServiceCallsDeny,
	}
	app := state.App{WorkloadName: "billing", WorkloadClass: state.WorkloadClassHTTP}
	got := diffFieldsChanged(app, workload, "")
	want := []string{"preview_service_calls_policy"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changed fields = %v, want %v", got, want)
	}
}
