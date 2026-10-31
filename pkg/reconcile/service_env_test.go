package reconcile

import (
	"reflect"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261025093331: a project created before the project default, from a
// repository without Compose, moves over by declaring it in its project file.
func TestProjectConfigMovesAnExistingDeclaredAppToProject(t *testing.T) {
	scan, err := reposcan.Scan(fstest.MapFS{
		"services/api/package.json": {Data: []byte(`{"name":"api","scripts":{"start":"node index.js"}}`)},
		".gregale/project.yaml":     {Data: []byte("service_policy: project\n")},
	})
	if err != nil || len(scan.Workloads) != 1 {
		t.Fatalf("scan = %+v, %v", scan.Workloads, err)
	}
	app := state.App{WorkloadName: "api", WorkloadClass: state.WorkloadClassHTTP,
		Manifest: state.AppManifest{ServiceBindingPolicy: api.ServiceBindingPolicyDeclared}}
	if got := serviceBindingPolicyForExistingWorkload(scan.Workloads[0], app.Manifest.ServiceBindingPolicy); got != api.ServiceBindingPolicyProject {
		t.Fatalf("reapplied policy = %q, want project", got)
	}
	if got := diffFieldsChanged(app, scan.Workloads[0], ""); !slices.Contains(got, "service_binding_policy") {
		t.Fatalf("changed fields = %v, want service_binding_policy", got)
	}
}

func TestPartialMigrationPreservesOnlyOwnedProductionServiceReferences(t *testing.T) {
	scan := reposcan.Result{Workloads: []reposcan.Workload{{Name: "web", DependsOn: []string{"api", "external"}}}, DependencyReferences: []reposcan.Workload{{Name: "api"}, {Name: "external"}}}
	project := state.Project{ID: "project", AccountID: "creator"}
	app := state.App{ID: "api-app", AccountID: "creator", ProjectID: "project", Slug: "shop-api", WorkloadName: "api"}
	for _, tc := range []struct {
		name   string
		target state.App
		want   bool
	}{
		{"production", app, true},
		{"foreign account", func() state.App { a := app; a.AccountID = "other"; return a }(), false},
		{"foreign project", func() state.App { a := app; a.ProjectID = "other"; return a }(), false},
		{"preview", func() state.App { a := app; a.PreviewOfSlug = "shop-api"; return a }(), false},
		{"deleted", func() state.App { a := app; a.Status = state.AppDeleted; return a }(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			available := availableServiceNames(scan, project, []state.App{tc.target})
			_, ok := available["api"]
			if ok != tc.want {
				t.Fatalf("reference availability=%v", ok)
			}
			if _, ok := available["external"]; ok {
				t.Fatal("external source reference gained an internal route")
			}
		})
	}
}

func TestServiceEnvForWorkload(t *testing.T) {
	got := serviceEnvForWorkloadWithAvailable(
		map[string]string{
			"APP_MODE":                      "prod",
			"GREGALE_SERVICE_OLD_URL":       "stale",
			"GREGALE_SERVICE_OLD_HTTPS_URL": "stale",
		},
		reposcan.Workload{Name: "api", DependsOn: []string{"db", "cache"}},
		map[string]struct{}{"db": {}, "cache": {}},
	)
	want := map[string]string{
		"APP_MODE":                        "prod",
		"GREGALE_SERVICE_DB_URL":          "http://db.svc.gregale:10081",
		"GREGALE_SERVICE_DB_HTTPS_URL":    "https://db.internal",
		"GREGALE_SERVICE_CACHE_URL":       "http://cache.svc.gregale:10081",
		"GREGALE_SERVICE_CACHE_HTTPS_URL": "https://cache.internal",
		"GREGALE_SERVICE_DB_HOST":         "db.svc.gregale",
		"GREGALE_SERVICE_CACHE_HOST":      "cache.svc.gregale",
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

func TestServiceReliabilityFollowsAvailableBindingsAndPreservesOmittedSource(t *testing.T) {
	existing := map[string]api.ServiceReliabilityPolicy{"billing": {TimeoutMS: 1200}, "old": {TimeoutMS: 500}}
	w := reposcan.Workload{Name: "api", DependsOn: []string{"billing", "managed"}}
	available := map[string]struct{}{"billing": {}}
	got := serviceReliabilityForWorkload(w, available, existing)
	if !reflect.DeepEqual(got, map[string]api.ServiceReliabilityPolicy{"billing": {TimeoutMS: 1200}}) {
		t.Fatalf("omitted source policy = %#v", got)
	}
	w.ServiceReliability = map[string]api.ServiceReliabilityPolicy{"billing": {MaxAttempts: 1}, "managed": {TimeoutMS: 900}}
	got = serviceReliabilityForWorkload(w, available, existing)
	if !reflect.DeepEqual(got, map[string]api.ServiceReliabilityPolicy{"billing": {MaxAttempts: 1}}) {
		t.Fatalf("explicit source policy = %#v", got)
	}
}

func TestServiceEnvHTTPSFirstUsesInternalAliasForCanonicalURL(t *testing.T) {
	got := serviceEnvForWorkloadWithAvailable(nil,
		reposcan.Workload{
			Name:                    "api",
			DependsOn:               []string{"billing"},
			ServiceBindingTransport: reposcan.ServiceBindingTransportHTTPS,
		},
		map[string]struct{}{"billing": {}},
	)
	want := map[string]string{
		"GREGALE_SERVICE_BILLING_URL":       "https://billing.internal",
		"GREGALE_SERVICE_BILLING_HTTPS_URL": "https://billing.internal",
		"GREGALE_SERVICE_BILLING_HOST":      "billing.svc.gregale",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("HTTPS-first service env = %#v, want %#v", got, want)
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
			"GREGALE_SERVICE_DB_URL": "http://db.svc.gregale:10081",
		}},
	}
	got := diffFieldsChanged(app, workload, "", map[string]struct{}{"api": {}, "db": {}})
	want := []string{"service_env", "service_bindings"}
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

func TestServiceBindingPolicyProjectIsKeptAndPolicyFromNewerReleaseSurvivesReapply(t *testing.T) {
	workload := reposcan.Workload{Name: "api"}
	app := state.App{WorkloadName: "api", WorkloadClass: state.WorkloadClassHTTP,
		Manifest: state.AppManifest{ServiceBindingPolicy: api.ServiceBindingPolicyProject}}
	if got := diffFieldsChanged(app, workload, ""); len(got) != 0 {
		t.Fatalf("project reapply changed fields = %v, want none", got)
	}
	if got := serviceBindingPolicyForExistingWorkload(workload, app.Manifest.ServiceBindingPolicy); got != api.ServiceBindingPolicyProject {
		t.Fatalf("existing project policy = %q, want project", got)
	}

	// A rolled-back control plane must not rewrite a newer release's policy
	// as declared: the narrowing would outlive the rollback.
	app.Manifest.ServiceBindingPolicy = "future-policy"
	if got := serviceBindingPolicyForExistingWorkload(workload, app.Manifest.ServiceBindingPolicy); got != "future-policy" {
		t.Fatalf("unknown stored policy = %q, want it kept", got)
	}
	if got := diffFieldsChanged(app, workload, ""); len(got) != 0 {
		t.Fatalf("unknown stored policy changed fields = %v, want none", got)
	}

	workload.ServiceBindingPolicy = reposcan.ServiceBindingPolicyProject
	if got := serviceBindingPolicyForNewWorkload(workload); got != api.ServiceBindingPolicyProject {
		t.Fatalf("new workload opting into project = %q", got)
	}
	app.Manifest.ServiceBindingPolicy = api.ServiceBindingPolicyDeclared
	if got := diffFieldsChanged(app, workload, ""); !reflect.DeepEqual(got, []string{"service_binding_policy"}) {
		t.Fatalf("declared to project changed fields = %v", got)
	}
}

func TestDiffFieldsChangedDetectsHTTPSFirstTransportAndPreservesOmittedMode(t *testing.T) {
	workload := reposcan.Workload{
		Name:                    "api",
		DependsOn:               []string{"db"},
		ServiceBindingPolicy:    reposcan.ServiceBindingPolicyDeclared,
		ServiceBindingTransport: reposcan.ServiceBindingTransportHTTPS,
	}
	app := state.App{
		WorkloadName:  "api",
		WorkloadClass: state.WorkloadClassHTTP,
		Manifest: state.AppManifest{
			ServiceBindings:      []api.AppServiceBinding{{Binding: "GREGALE_SERVICE_DB_URL", Service: "db"}},
			ServiceBindingPolicy: api.ServiceBindingPolicyDeclared,
			Env: map[string]string{
				"GREGALE_SERVICE_DB_URL":       "http://db.svc.gregale:10081",
				"GREGALE_SERVICE_DB_HTTPS_URL": "https://db.internal",
				"GREGALE_SERVICE_DB_HOST":      "db.svc.gregale",
			},
		},
	}
	got := diffFieldsChanged(app, workload, "", map[string]struct{}{"api": {}, "db": {}})
	want := []string{"service_env", "service_binding_transport"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("HTTPS transport changes = %v, want %v", got, want)
	}

	app.Manifest.ServiceBindingTransport = api.ServiceBindingTransportHTTPS
	app.Manifest.Env["GREGALE_SERVICE_DB_URL"] = "https://db.internal"
	workload.ServiceBindingTransport = "" // omitted source preserves an opted-in existing app
	if got := diffFieldsChanged(app, workload, "", map[string]struct{}{"api": {}, "db": {}}); len(got) != 0 {
		t.Fatalf("omitted transport changed an existing HTTPS app: %v", got)
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
