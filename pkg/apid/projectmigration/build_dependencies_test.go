package projectmigration

import (
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBuildDependenciesRetainSelectedAPIOnFrontendRetry(t *testing.T) {
	b, m, in, apps := fixture(t)
	in.ID = uuid.NewString()
	in.BuildDependencies = map[string][]string{"web": {"api"}}
	r, err := b.Create(t.Context(), in, publicPlan(), "shop", "source-hash")
	if err != nil {
		t.Fatal(err)
	}
	in.BuildDependencies["web"][0] = "mutated"
	run, err := b.Begin(t.Context(), r.Report.ID, "source-hash", "shop", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.InitialConfiguration(nil, []reposcan.Workload{{Name: "api"}, {Name: "web", DependsOn: []string{"api"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := run.Prepare(t.Context(), apps[0].ProjectID, apps); err != nil {
		t.Fatal(err)
	}
	r, _ = m.GetProjectMigration(t.Context(), b.AccountID, r.Report.ID)
	for _, s := range r.Report.Services {
		if _, err := m.CreateDeployment(t.Context(), state.Deployment{ID: s.DeploymentID, AppID: s.AppID, Kind: state.DeploymentKindTarball, BuildDependencies: run.BuildDependencies(s.AppID)}); err != nil {
			t.Fatal(err)
		}
		if _, err := m.CreateBuildWithID(t.Context(), s.BuildID, s.DeploymentID, state.DeploymentKindTarball, 0, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.MarkDeploymentLive(t.Context(), r.Report.Services[0].DeploymentID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetDeploymentFailed(t.Context(), r.Report.Services[1].DeploymentID, "test_failure", "test"); err != nil {
		t.Fatal(err)
	}
	if err := run.Finish(t.Context(), nil, false); err != nil {
		t.Fatal(err)
	}
	saved, err := b.Get(t.Context(), r.Report.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(saved.Services[1].BuildDependencies, []string{"api"}) {
		t.Fatal("saved build intent changed")
	}
	retry, err := b.BeginReviewed(t.Context(), r.Report.ID, "source-hash", "shop", nil, saved.Version, []string{"web"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := retry.Prepare(t.Context(), apps[0].ProjectID, apps); err != nil {
		t.Fatal(err)
	}
	pins := retry.BuildDependencies(apps[1].ID)
	if len(pins) != 1 || pins[0].DeploymentID != r.Report.Services[0].DeploymentID || pins[0].AppID != apps[0].ID {
		t.Fatalf("retry lost healthy API: %+v", pins)
	}
	if !slices.Equal(retry.Names(), []string{"web"}) {
		t.Fatal("retry included accepted API")
	}
}

func TestBuildDependenciesRejectUnreviewedTargets(t *testing.T) {
	b, _, base, _ := fixture(t)
	for _, deps := range []map[string][]string{{"other": {"api"}}, {"web": {"other"}}, {"web": {"web"}}, {"web": {"api", "api"}}, {"api": {"web"}}} {
		in := base
		in.ID = uuid.NewString()
		in.BuildDependencies = deps
		if _, err := b.Create(t.Context(), in, publicPlan(), "shop", "source-hash"); err == nil {
			t.Fatalf("accepted invalid build wait: %v", deps)
		}
	}
}
