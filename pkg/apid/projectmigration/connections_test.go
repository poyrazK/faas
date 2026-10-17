package projectmigration

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

func connectionPlan() api.PlanResponse {
	return api.PlanResponse{CanApply: true, Workloads: []api.PlanWorkload{{Name: "api", AppSlug: "shop-api", Class: "http"}, {Name: "web", AppSlug: "shop-web", Class: "http", DependsOn: []string{"api"}, RequiredEnvKeys: []string{"API_URL"}}}}
}

func TestMigrationConnectionsAreExplicitSelectedDependencies(t *testing.T) {
	b, _, base, _ := fixture(t)
	for _, tc := range []struct {
		name        string
		connections api.ProjectServiceConnections
		values      api.ProjectInitialSecrets
	}{
		{"unknown caller", api.ProjectServiceConnections{"other": {"API_URL": "api"}}, nil},
		{"unknown target", api.ProjectServiceConnections{"web": {"API_URL": "other"}}, nil},
		{"undeclared", api.ProjectServiceConnections{"api": {"WEB_URL": "web"}}, nil},
		{"self", api.ProjectServiceConnections{"web": {"WEB_URL": "web"}}, nil},
		{"platform", api.ProjectServiceConnections{"web": {"GREGALE_API_URL": "api"}}, nil},
		{"browser", api.ProjectServiceConnections{"web": {"NEXT_PUBLIC_API_URL": "api"}}, nil},
		{"duplicate value", api.ProjectServiceConnections{"web": {"API_URL": "api"}}, api.ProjectInitialSecrets{"web": {"API_URL": "private-never-echo"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			in.ID, in.ServiceConnections, in.InitialSecrets = uuid.NewString(), tc.connections, tc.values
			_, err := b.Create(t.Context(), in, connectionPlan(), "shop", "source-hash")
			if err == nil || strings.Contains(err.Error(), "private-never-echo") {
				t.Fatalf("invalid mapping: %v", err)
			}
		})
	}
	in := base
	in.ID, in.Only, in.ServiceConnections = uuid.NewString(), []string{"web"}, api.ProjectServiceConnections{"web": {"API_URL": "api"}}
	plan := connectionPlan()
	plan.Workloads = plan.Workloads[1:]
	if _, err := b.Create(t.Context(), in, plan, "shop", "source-hash"); err == nil {
		t.Fatal("connection to an unselected service accepted")
	}
}

func TestMigrationConnectionIntentSurvivesRetryWithoutValues(t *testing.T) {
	b, m, in, _ := fixture(t)
	in.ID, in.InitialSecrets = uuid.NewString(), nil
	in.ServiceConnections = api.ProjectServiceConnections{"web": {"API_URL": "api"}}
	record, err := b.Create(t.Context(), in, connectionPlan(), "shop", "source-hash")
	if err != nil {
		t.Fatal(err)
	}
	// The store must own its copy of the submitted non-secret mapping.
	in.ServiceConnections["web"]["API_URL"] = "other"
	run, err := b.BeginReviewed(t.Context(), record.Report.ID, "source-hash", "shop", nil, record.Version, []string{"web"})
	if err != nil {
		t.Fatal(err)
	}
	workloads := []reposcan.Workload{{Name: "web", DependsOn: []string{"api"}}}
	values, err := run.InitialConfiguration(api.ProjectInitialSecrets{"web": {"TOKEN": "private-never-persist"}}, workloads)
	if err != nil || values["web"]["API_URL"] != "https://api.internal" || values["web"]["TOKEN"] != "private-never-persist" || values["api"] != nil {
		t.Fatalf("resolve: %d callers %v", len(values), err)
	}
	if err := run.Finish(t.Context(), nil, true); err != nil {
		t.Fatal(err)
	}
	after, err := m.GetProjectMigration(t.Context(), b.AccountID, record.Report.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(after)
	if strings.Contains(string(raw), "https://") || strings.Contains(string(raw), "private-never-persist") {
		t.Fatal("configuration value entered durable metadata")
	}
	if after.Report.Services[1].ServiceConnections["API_URL"] != "api" {
		t.Fatal("lost saved connection")
	}
	if _, err := b.BeginReviewed(t.Context(), after.Report.ID, "source-hash", "shop", api.ProjectInitialSecrets{"web": {"API_URL": "private-override"}}, after.Version, []string{"web"}); err == nil {
		t.Fatal("saved mapping replaced by supplied value")
	}
	unchanged, _ := m.GetProjectMigration(t.Context(), b.AccountID, record.Report.ID)
	if unchanged.Version != after.Version || !unchanged.LeaseUntil.IsZero() {
		t.Fatal("invalid override mutated the operation")
	}
	retry, err := b.BeginReviewed(t.Context(), after.Report.ID, "source-hash", "shop", nil, after.Version, []string{"web"})
	if err != nil {
		t.Fatal(err)
	}
	values, err = retry.InitialConfiguration(nil, workloads)
	if err != nil || values["web"]["API_URL"] != "https://api.internal" {
		t.Fatal("retry did not reuse saved connection")
	}
	if _, err := retry.InitialConfiguration(nil, []reposcan.Workload{{Name: "web"}}); err == nil {
		t.Fatal("undeclared saved dependency accepted")
	}
}
