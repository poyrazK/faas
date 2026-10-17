package projectsetup

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSuggestedAppSlugsAreValidStableAndDistinct(t *testing.T) {
	for _, tc := range [][2]string{{"shop", "api"}, {"x", "y"}, {"tag", "web"}, {"pr-7", "shop"}, {"shop", "API_service"}, {strings.Repeat("long-", 12), strings.Repeat("service", 20)}} {
		got := SuggestedAppSlug(tc[0], tc[1])
		if !api.ValidAppSlug(got) || api.IsReservedAppSlug(got) || got != SuggestedAppSlug(tc[0], tc[1]) {
			t.Fatalf("invalid or unstable suggestion for %q: %q", tc, got)
		}
	}
	if got := SuggestedAppSlug("shop", "api"); got != "shop-api" {
		t.Fatal(got)
	}
	for _, names := range [][2]string{{"API_service", "api-service"}, {strings.Repeat("a", 70) + "x", strings.Repeat("a", 70) + "y"}} {
		if SuggestedAppSlug("shop", names[0]) == SuggestedAppSlug("shop", names[1]) {
			t.Fatal("suggestions collapsed distinct services")
		}
	}
}

func TestAppSlugsPreserveRepositoryAndExistingIdentity(t *testing.T) {
	workloads := []reposcan.Workload{{Name: "api"}, {Name: "web", DependsOn: []string{"api"}}}
	apps := []state.App{{ProjectID: "project", WorkloadName: "api", Slug: "pr-7-existing-api", PreviewOfSlug: "existing-api"}, {ProjectID: "project", WorkloadName: "api", Slug: "existing-api"}}
	slugs, problem := AppSlugs(workloads, apps, "project", "shop", map[string]string{"web": "custom-web"}, true)
	if problem != nil || slugs["api"] != "existing-api" || slugs["web"] != "custom-web" {
		t.Fatalf("%v %v", slugs, problem)
	}
	SetAppSlugs(workloads, slugs)
	if workloads[1].Name != "web" || workloads[1].DependsOn[0] != "api" {
		t.Fatal("repository identity changed")
	}
	for _, overrides := range []map[string]string{{"unknown": "shop-api"}, {"api": ""}, {"API": "shop-api"}} {
		if _, p := AppSlugs(workloads, nil, "", "shop", overrides, true); p == nil {
			t.Fatalf("accepted %v", overrides)
		}
	}
}

func TestLegacyGenericMigrationIdentityRemainsResumable(t *testing.T) {
	for _, tc := range []struct {
		name               string
		apps               []state.App
		only               map[string]bool
		wantName, wantSlug string
	}{
		{name: "fresh scan", wantName: "web"},
		{name: "explicit legacy receipt before app creation", only: map[string]bool{"shop": true}, wantName: "shop", wantSlug: "shop"},
		{name: "existing legacy allocation", apps: []state.App{{ProjectID: "project", WorkloadName: "shop", Slug: "legacy-custom"}}, wantName: "shop", wantSlug: "legacy-custom"},
		{name: "new allocation keeps repository name", apps: []state.App{{ProjectID: "project", WorkloadName: "web", Slug: "shop-web"}}, wantName: "web"},
		{name: "preview is not a production identity", apps: []state.App{{ProjectID: "project", WorkloadName: "shop", Slug: "pr-7-shop", PreviewOfSlug: "shop"}}, wantName: "web"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workloads := []reposcan.Workload{{Name: "web", DetectedBy: reposcan.Detection{Detector: "procfile"}}}
			PreserveLegacyMigrationService(workloads, tc.apps, "project", "shop", tc.only)
			if workloads[0].Name != tc.wantName || workloads[0].AppSlug != tc.wantSlug {
				t.Fatalf("unexpected identity: %+v", workloads[0])
			}
		})
	}
}
