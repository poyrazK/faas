package reconcile

import (
	"context"
	"errors"
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

func TestAllocateAppSlugsPrefixesNamesThatCannotBeSlugs(t *testing.T) {
	rows := map[string]state.App{
		"worker":    {Slug: "worker", AccountID: "acct-b", Status: state.AppActive},
		"tombstone": {Slug: "tombstone", AccountID: "acct-a", Status: state.AppDeleted},
		"foreign-x": {Slug: "foreign-x", AccountID: "acct-b", Status: state.AppDeleted},
	}
	lookup := func(_ context.Context, slug string) (state.App, error) {
		if app, ok := rows[slug]; ok {
			return app, nil
		}
		return state.App{}, state.ErrNotFound
	}
	accountApps := []state.App{
		{Slug: "mine", AccountID: "acct-a", Status: state.AppActive},
		{Slug: "shop-ledger", AccountID: "acct-a", ProjectID: "project", WorkloadName: "ledger"},
	}
	workloads := []reposcan.Workload{
		{Name: "api"}, {Name: "ui"}, {Name: "worker"}, {Name: "mine"}, {Name: "tombstone"},
		{Name: "foreign-x"}, {Name: "fresh"}, {Name: "ledger"}, {Name: "billing", AppSlug: "my-billing"},
	}
	got, err := AllocateAppSlugs(t.Context(), lookup, workloads, accountApps, "acct-a", "project", "shop")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"api":       "shop-api",    // reserved
		"ui":        "shop-ui",     // too short to be a slug
		"worker":    "shop-worker", // another account's app
		"mine":      "shop-mine",   // another of the account's apps
		"tombstone": "",            // the account's own app in its grace window keeps the restore offer
		"foreign-x": "shop-foreign-x",
		"fresh":     "",
		"ledger":    "shop-ledger", // existing member
		"billing":   "my-billing",  // explicit slug
	}
	for _, w := range got {
		if w.AppSlug != want[w.Name] {
			t.Errorf("%s: AppSlug = %q, want %q", w.Name, w.AppSlug, want[w.Name])
		}
	}
	if workloads[0].AppSlug != "" {
		t.Fatal("allocation mutated the caller's workloads")
	}

	unscoped, err := AllocateAppSlugs(t.Context(), lookup, []reposcan.Workload{{Name: "api"}}, nil, "acct-a", "", "")
	if err != nil || unscoped[0].AppSlug != "" {
		t.Fatalf("without a project slug: %+v %v", unscoped, err)
	}
	boom := errors.New("pool closed")
	failing := func(context.Context, string) (state.App, error) { return state.App{}, boom }
	if _, err := AllocateAppSlugs(t.Context(), failing, []reposcan.Workload{{Name: "fresh"}}, nil, "acct-a", "", "shop"); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the lookup failure", err)
	}
}

func TestReconcilePrefixesReservedServiceNames(t *testing.T) {
	store := newFakeStore()
	_, project := seedProject(t, store, state.ProjectScanSourceCompose, "main")
	svc := NewService(store, newFakeAuditor(store), nil)
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{
		{Name: "api", Source: "compose.yaml: api", Class: reposcan.ClassHTTP, Tier: reposcan.TierCompose},
		{Name: "web", Source: "compose.yaml: web", Class: reposcan.ClassHTTP, Tier: reposcan.TierCompose, DependsOn: []string{"api"}},
	}}
	created, err := svc.Reconcile(t.Context(), project, scan, "", "", nil)
	if err != nil || len(created.Added) != 2 {
		t.Fatalf("%+v %v", created, err)
	}
	slugs := map[string]string{}
	for _, app := range created.Added {
		slugs[app.WorkloadName] = app.Slug
		if app.WorkloadName == "web" && (len(app.Manifest.ServiceBindings) != 1 || app.Manifest.ServiceBindings[0].Service != "api") {
			t.Fatalf("web bindings = %+v, want the repository name", app.Manifest.ServiceBindings)
		}
	}
	if slugs["api"] != project.Slug+"-api" || slugs["web"] != "web" {
		t.Fatalf("slugs = %v", slugs)
	}
	updated, err := svc.Reconcile(t.Context(), project, scan, "", "", nil)
	if err != nil || len(updated.Added)+len(updated.Changed)+len(updated.Removed) != 0 {
		t.Fatalf("subsequent sync changed allocations: %+v %v", updated, err)
	}
}
