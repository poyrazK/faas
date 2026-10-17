package projectmigration

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

func publicPlan() api.PlanResponse {
	p := connectionPlan()
	p.Workloads[0].PublicURL = "https://shop-api.example.com"
	p.Workloads[1].RequiredEnvKeys = []string{"VITE_API_URL"}
	return p
}

func TestPublicMigrationURLsAreExplicitAndRestoredBeforeBuild(t *testing.T) {
	b, m, in, apps := fixture(t)
	in.ID, in.InitialSecrets = uuid.NewString(), nil
	in.PublicServiceConnections = api.ProjectServiceConnections{"web": {"VITE_API_URL": "api"}}
	in.PublicBuildURLs = api.ProjectPublicBuildURLs{"web": {"NEXT_PUBLIC_AUTH_URL": "https://auth.example.com/v1"}}
	record, err := b.Create(t.Context(), in, publicPlan(), "shop", "source-hash")
	if err != nil {
		t.Fatal(err)
	}
	in.PublicBuildURLs["web"]["NEXT_PUBLIC_AUTH_URL"] = "https://changed.example.com"
	run, err := b.BeginReviewed(t.Context(), record.Report.ID, "source-hash", "shop", nil, record.Version, []string{"web"})
	if err != nil {
		t.Fatal(err)
	}
	values, err := run.InitialConfiguration(nil, []reposcan.Workload{{Name: "web", DependsOn: []string{"api"}}})
	if err != nil || len(values) != 0 {
		t.Fatalf("public URLs entered secret values: %v", err)
	}
	if _, err := run.Prepare(t.Context(), apps[0].ProjectID, apps); err != nil {
		t.Fatal(err)
	}
	app, _ := m.AppByID(t.Context(), apps[1].ID)
	if app.Manifest.PublicBuildURLs["VITE_API_URL"] != publicPlan().Workloads[0].PublicURL || app.Manifest.PublicBuildURLs["NEXT_PUBLIC_AUTH_URL"] != "https://auth.example.com/v1" {
		t.Fatal("public configuration was not saved before queueing")
	}
	if err := run.Finish(t.Context(), nil, true); err != nil {
		t.Fatal(err)
	}
	after, _ := m.GetProjectMigration(t.Context(), b.AccountID, record.Report.ID)
	if after.Report.Services[1].PublicServiceConnections["VITE_API_URL"] != "api" {
		t.Fatal("public mapping lost")
	}
	before, _ := json.Marshal(after)
	if _, err := b.BeginReviewed(t.Context(), record.Report.ID, "source-hash", "shop", api.ProjectInitialSecrets{"web": {"VITE_API_URL": "private-override"}}, after.Version, []string{"web"}); err == nil {
		t.Fatal("secret override accepted")
	}
	unchanged, _ := m.GetProjectMigration(t.Context(), b.AccountID, record.Report.ID)
	raw, _ := json.Marshal(unchanged)
	if string(raw) != string(before) || strings.Contains(string(raw), "private-override") {
		t.Fatal("invalid override mutated public configuration")
	}
}

func TestPublicMigrationURLValidation(t *testing.T) {
	b, _, base, _ := fixture(t)
	for _, tc := range []struct {
		name        string
		connections api.ProjectServiceConnections
		urls        api.ProjectPublicBuildURLs
		secrets     api.ProjectInitialSecrets
	}{
		{"secret key", nil, api.ProjectPublicBuildURLs{"web": {"DATABASE_URL": "https://db.example.com"}}, nil},
		{"internal", nil, api.ProjectPublicBuildURLs{"web": {"VITE_API_URL": "https://api.internal"}}, nil},
		{"credentials", nil, api.ProjectPublicBuildURLs{"web": {"VITE_API_URL": "https://private:secret@api.example.com"}}, nil},
		{"foreign caller", nil, api.ProjectPublicBuildURLs{"other": {"VITE_API_URL": "https://api.example.com"}}, nil},
		{"foreign target", api.ProjectServiceConnections{"web": {"VITE_API_URL": "other"}}, nil, nil},
		{"undeclared", api.ProjectServiceConnections{"api": {"VITE_API_URL": "web"}}, nil, nil},
		{"self", api.ProjectServiceConnections{"web": {"VITE_API_URL": "web"}}, nil, nil},
		{"two public sources", api.ProjectServiceConnections{"web": {"VITE_API_URL": "api"}}, api.ProjectPublicBuildURLs{"web": {"VITE_API_URL": "https://api.example.com"}}, nil},
		{"secret collision", api.ProjectServiceConnections{"web": {"VITE_API_URL": "api"}}, nil, api.ProjectInitialSecrets{"web": {"VITE_API_URL": "private-value"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			in.ID, in.InitialSecrets, in.PublicServiceConnections, in.PublicBuildURLs = uuid.NewString(), tc.secrets, tc.connections, tc.urls
			if _, err := b.Create(t.Context(), in, publicPlan(), "shop", "source-hash"); err == nil || strings.Contains(err.Error(), "private-value") {
				t.Fatalf("invalid public configuration: %v", err)
			}
		})
	}
}

func TestPublicMigrationDoesNotLeaveAnExistingRuntimeSecretShadowingTheBuild(t *testing.T) {
	b, m, in, apps := fixture(t)
	in.ID, in.InitialSecrets = uuid.NewString(), nil
	in.PublicServiceConnections = api.ProjectServiceConnections{"web": {"VITE_API_URL": "api"}}
	record, err := b.Create(t.Context(), in, publicPlan(), "shop", "source-hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.UpsertAppSecret(t.Context(), b.AccountID, apps[1].ID, "VITE_API_URL", []byte("sealed-old-value")); err != nil {
		t.Fatal(err)
	}
	run, err := b.BeginReviewed(t.Context(), record.Report.ID, "source-hash", "shop", nil, record.Version, []string{"web"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Prepare(t.Context(), apps[0].ProjectID, apps); err == nil || !strings.Contains(err.Error(), "existing runtime secret") {
		t.Fatal("shadowed public URL accepted", err)
	}
	app, _ := m.AppByID(t.Context(), apps[1].ID)
	if len(app.Manifest.PublicBuildURLs) != 0 {
		t.Fatal("invalid public intent was persisted")
	}
}
