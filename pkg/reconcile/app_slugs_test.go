package reconcile

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMappedAppSlugAdmission(t *testing.T) {
	for _, tc := range []struct{ name, slug, want string }{
		{"api", "shop-api", ""}, {"API_service", "shop-service", ""},
		{"api", "api", "reserved"}, {"api", "Bad Slug", "invalid"},
		{"api", "taken", "outside this project"},
	} {
		t.Run(tc.slug, func(t *testing.T) {
			reasons := WorkloadAdmissionReasons([]reposcan.Workload{{Name: tc.name, AppSlug: tc.slug}}, []state.App{{Slug: "taken", ProjectID: "elsewhere"}}, "project")
			if tc.want == "" && len(reasons) != 0 || tc.want != "" && !strings.Contains(strings.Join(reasons, ";"), tc.want) {
				t.Fatal(reasons)
			}
		})
	}
	workloads := []reposcan.Workload{{Name: "api", AppSlug: "shared"}, {Name: "web", AppSlug: "shared"}}
	if reasons := WorkloadAdmissionReasons(workloads, nil, ""); len(reasons) != 1 || !strings.Contains(reasons[0], "duplicate") {
		t.Fatal(reasons)
	}
	apps := []state.App{{ProjectID: "project", WorkloadName: "api", Slug: "original"}}
	if reasons := WorkloadAdmissionReasons([]reposcan.Workload{{Name: "api", AppSlug: "replacement"}}, apps, "project"); len(reasons) == 0 {
		t.Fatal("member renamed")
	}
	looked := ""
	holders, err := SlugHolders(t.Context(), func(_ context.Context, slug string) (state.App, error) {
		looked = slug
		return state.App{Slug: slug, AccountID: "foreign"}, nil
	}, []reposcan.Workload{{Name: "api", AppSlug: "shop-api"}}, nil)
	if err != nil || looked != "shop-api" || len(SlugHolderReasons("mine", []reposcan.Workload{{Name: "api", AppSlug: "shop-api"}}, holders)) != 1 {
		t.Fatal(looked, err)
	}
}

func TestReconcileMappedMembersRetainNamesAndDependencies(t *testing.T) {
	store := newFakeStore()
	_, project := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	svc := NewService(store, newFakeAuditor(store), nil)
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{
		{Name: "api", AppSlug: "shop-api", Source: "compose.yaml: api", Class: reposcan.ClassHTTP, Tier: reposcan.TierCompose},
		{Name: "web", AppSlug: "shop-web", Source: "compose.yaml: web", Class: reposcan.ClassHTTP, Tier: reposcan.TierCompose, DependsOn: []string{"api"}},
	}}
	created, err := svc.Reconcile(t.Context(), project, scan, "", "", nil)
	if err != nil || len(created.Added) != 2 {
		t.Fatalf("%+v %v", created, err)
	}
	for _, app := range created.Added {
		if app.Slug != "shop-"+app.WorkloadName {
			t.Fatal(app)
		}
		if app.WorkloadName == "web" && (len(app.Manifest.ServiceBindings) != 1 || app.Manifest.ServiceBindings[0].Service != "api") {
			t.Fatal("dependency identity lost")
		}
	}
	for i := range scan.Workloads {
		scan.Workloads[i].AppSlug = ""
	}
	updated, err := svc.Reconcile(t.Context(), project, scan, "", "", nil)
	if err != nil || len(updated.Added)+len(updated.Changed)+len(updated.Removed) != 0 {
		t.Fatalf("subsequent sync changed allocations: %+v %v", updated, err)
	}
}
