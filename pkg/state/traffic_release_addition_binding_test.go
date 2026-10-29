// adr: 570
package state

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// A safe release reserves a new service as an ordinary app, so its primary
// hostname is a new binding with no legacy allowance.
func testTrafficReleaseAdditionPublication(t *testing.T, store Store, seed func(EdgeRule), intent func() string) {
	t.Helper()
	ctx := t.Context()
	require := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	account, err := store.CreateAccount(ctx, "release-addition@example.test", api.PlanPro)
	require(err)
	project, err := store.CreateProject(ctx, Project{AccountID: account.ID, Slug: "shop"})
	require(err)
	web, err := store.CreateApp(ctx, App{AccountID: account.ID, ProjectID: project.ID, Slug: "web", WorkloadName: "web", WorkloadClass: WorkloadClassHTTP, Manifest: AppManifest{RevisionPinTTLSeconds: 1800}})
	require(err)
	old, err := store.CreateDeployment(ctx, Deployment{AppID: web.ID, Scope: "production", Kind: DeploymentKindTarball})
	require(err)
	require(store.MarkDeploymentLive(ctx, old.ID))
	baseline, err := store.(ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, []ProjectReleaseMember{{AppID: web.ID, DeploymentID: old.ID}})
	require(err)
	draft := App{AccountID: account.ID, ProjectID: project.ID, Slug: "metrics", WorkloadName: "metrics", RootDir: "metrics", WorkloadClass: WorkloadClassHTTP, PublicAuthMode: api.AppPublicAuthModeOpen, Manifest: AppManifest{RevisionPinTTLSeconds: 1800}}
	safe := &api.ProjectSafeRelease{AddServices: true, Status: "building", PreviousReleaseID: baseline.ID, TTLSeconds: 1800, Deadline: time.Now().Add(time.Hour),
		Additions: []api.ProjectReleaseAddition{{Slug: draft.Slug, RootDir: draft.RootDir, Fingerprint: ProjectReleaseAdditionFingerprint(draft)}}}
	attempt := strings.Repeat("a", 32)
	receipt := api.ProjectSubmissionReceipt{AttemptID: attempt, ProjectSlug: project.Slug, Environment: "production", Status: "processing",
		Apply: api.ApplyResponse{ProjectID: project.ID, PlanResponse: api.PlanResponse{SafeRelease: safe}}}
	_, err = store.(ProjectSubmissionStore).ReserveProjectSubmission(ctx, account.ID, attempt, strings.Repeat("b", 64), receipt)
	require(err)
	host := "metrics.apps.example.test"
	legacy := publicationLegacyRule(account, web, host)
	seed(legacy)
	before := intent()
	stage := func() ([]App, error) {
		return store.(ProjectReleaseAdditionsStore).StageProjectReleaseAdditions(ctx, account.ID, attempt, project.ID, safe, []App{draft})
	}
	_, err = stage()
	var aggregate *TrafficPolicyAggregateError
	if !errors.As(err, &aggregate) || aggregate.Scope != "host_rule_projection" || aggregate.Host != host {
		t.Fatalf("release addition published an oversized primary hostname: %v", err)
	}
	if before != intent() {
		t.Fatal("refused release addition changed app, reservation or submission intent")
	}
	require(store.DeleteEdgeRule(ctx, legacy.ID))
	apps, err := stage()
	if err != nil || len(apps) != 1 || apps[0].Slug != draft.Slug {
		t.Fatalf("release addition after repair: %+v %v", apps, err)
	}
}

func TestMemTrafficReleaseAdditionPublication(t *testing.T) {
	m := NewMemStore()
	m.trafficAppsSuffix = ".apps.example.test"
	testTrafficReleaseAdditionPublication(t, m, func(rule EdgeRule) { m.edgeRules[rule.ID] = rule }, func() string {
		return trafficTenantIntentDigest(t, []any{memTrafficAppIntent(t, m), m.projectReleaseAdditions, m.projectSubmissions})
	})
}
