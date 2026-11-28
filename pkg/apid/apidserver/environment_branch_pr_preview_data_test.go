// adr: 20261113121342 amendment 2026-11-26 — a pull request's branch follows
// each production database's previews setting: off gives it no database,
// empty a new empty database shared with the PR's app previews, and a reset
// starts that database again.
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// restorableProvider offers the point-in-time restore window a branch's
// parent database needs.
type restorableProvider struct {
	apidserver.SourceRefManagedPostgresProvider
}

func (p restorableProvider) Capabilities() managedpostgres.Capabilities {
	c := p.SourceRefManagedPostgresProvider.Capabilities()
	c.PointInTimeRestore = true
	c.MaxRestoreWindowSeconds = 3600
	return c
}

// branchPreviewDatabases wires managed PostgreSQL into e with the credential
// sink that writes app secrets, which is how a branch finds its parent's
// databases, and creates orders with a restore window, which a branch needs.
func branchPreviewDatabases(t *testing.T, e apidserver.TestEnv) (*managedpostgres.Service, *managedpostgres.BindingService, managedpostgres.Database) {
	t.Helper()
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{
		DefaultRegion: "eu", Defaults: map[string]string{"eu": "test"}, MaxDatabasesPerAccount: 3, ProvisioningEnabled: true,
		Backends: []managedpostgres.BackendConfig{{ID: "test", Driver: "test", Region: "eu", Namespace: "branch-previews"}},
	}, func(string) string { return "" }, map[string]managedpostgres.Factory{
		"test": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
			return restorableProvider{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	databases := managedpostgres.NewMemoryStore()
	service, err := managedpostgres.NewService(registry, databases, managedpostgres.ServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	sink, err := apidserver.NewAppSecretCredentialSink(*e.Store(), func() *age.X25519Recipient { return identity.Recipient() }, func() []byte { return []byte("0123456789abcdef0123456789abcdef") })
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := managedpostgres.NewBindingService(registry, databases, databases, sink, managedpostgres.BindingServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	(*e.S()).WithManagedPostgres(service, nil, bindings, nil, nil, nil)
	orders, err := service.Create(t.Context(), managedpostgres.CreateRequest{AccountID: e.Acct().ID, Name: "orders", Spec: managedpostgres.Spec{
		Region: "eu", PostgresMajor: 17, Class: managedpostgres.ClassDevelopment, Availability: managedpostgres.AvailabilitySingleZone,
		ScaleToZero: true, StorageLimitBytes: 1 << 30, RestoreWindowSeconds: 3600}})
	if err != nil || orders.State != managedpostgres.StateReady {
		t.Fatalf("create orders = %+v, %v", orders, err)
	}
	return service, bindings, orders
}

func TestEnvironmentBranchPRDataFollowsPreviews(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx := t.Context()
	service, bindings, orders := branchPreviewDatabases(t, e)
	srv, store, acct := *e.S(), *e.Store(), *e.Acct()
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "shop", ProductionBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "shop-web", WorkloadName: "web", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bindings.Create(ctx, managedpostgres.CreateBindingRequest{AccountID: acct.ID, DatabaseID: orders.ID, AppID: app.ID,
		Scope: "production", EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadWrite}); err != nil {
		t.Fatal(err)
	}
	parent, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:web"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, parent.ID, "/web.ext4", "layers/web.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{apidserver.RuntimeConfigEnvironmentBranches, apidserver.RuntimeConfigEnvironmentBranchPRPreviews} {
		if err := (*srv.RuntimeConfig()).Apply(key, json.RawMessage("true")); err != nil {
			t.Fatal(err)
		}
	}
	mode := "branch"
	if rec := e.Do(t, http.MethodPatch, "/v1/apps/shop-web/github/deployment-policy", api.GitHubDeploymentPolicyPatch{PreviewMode: &mode}, nil); rec.Code != http.StatusOK {
		t.Fatalf("PATCH preview_mode=branch = %d %s", rec.Code, rec.Body)
	}
	branchFor := func(t *testing.T, pullRequest int) state.ProjectEnvironmentBranch {
		t.Helper()
		if out, err := srv.EnsurePullRequestBranch(ctx, acct.ID, project.ID, pullRequest, time.Now().Add(48*time.Hour)); err != nil || !out.GetReady() {
			t.Fatalf("ensure #%d = %+v, %v", pullRequest, out, err)
		}
		// Past the restore lag, as the reconciler runs; a few passes let a
		// database and its binding finish provisioning.
		for pass := range 3 {
			if err := srv.ReconcileEnvironmentBranchData(ctx, time.Now().UTC().Add(time.Duration(pass+1)*time.Minute)); err != nil {
				t.Fatal(err)
			}
		}
		branch, err := store.ProjectEnvironmentBranchBySlug(ctx, acct.ID, project.ID, "pr-"+strconv.Itoa(pullRequest))
		if err != nil || branch.DataState != state.EnvironmentBranchDataReady {
			t.Fatalf("pr-%d after reconcile = %+v, %v", pullRequest, branch, err)
		}
		return branch
	}
	scoped := func(t *testing.T, databaseID, scope string) []managedpostgres.Binding {
		t.Helper()
		all, err := bindings.List(ctx, acct.ID, databaseID)
		if err != nil {
			t.Fatal(err)
		}
		var out []managedpostgres.Binding
		for _, binding := range all {
			if binding.Scope == scope {
				out = append(out, binding)
			}
		}
		return out
	}

	// Off, the default: production's data never reaches code under review.
	branchFor(t, 7)
	if got := scoped(t, orders.ID, "pr-7"); len(got) != 0 {
		t.Fatalf("pr-7 holds production's database: %+v", got)
	}
	if made, _ := service.PreviewDatabases(ctx, acct.ID, project.ID, 7); len(made) != 0 {
		t.Fatalf("an off database set up preview databases: %+v", made)
	}

	// Empty: a new empty database, shared with the PR's app previews, bound
	// under production's key.
	if rec := e.Do(t, http.MethodPut, "/v1/postgres/databases/"+orders.ID+"/previews", api.SetManagedPostgresPreviewsRequest{Previews: "empty"}, nil); rec.Code != http.StatusOK {
		t.Fatalf("set previews empty: %d %s", rec.Code, rec.Body)
	}
	branchFor(t, 8)
	made, err := service.PreviewDatabases(ctx, acct.ID, project.ID, 8)
	if err != nil || len(made) != 1 || made[0].Preview.SourceDatabaseID != orders.ID {
		t.Fatalf("pr-8 preview databases = %+v, %v", made, err)
	}
	if got := scoped(t, made[0].ID, "pr-8"); len(got) != 1 || got[0].AppID != app.ID || got[0].EnvironmentKey != "DATABASE_URL" {
		t.Fatalf("pr-8 bindings to its empty database = %+v", got)
	}
	if got := scoped(t, orders.ID, "pr-8"); len(got) != 0 {
		t.Fatalf("pr-8 holds production's database: %+v", got)
	}

	// A reset starts the empty database again.
	if rec := e.Do(t, http.MethodPost, "/v1/projects/shop/environments/pr-8/branch/reset", nil, nil); rec.Code >= 300 {
		t.Fatalf("reset pr-8 = %d %s", rec.Code, rec.Body)
	}
	for pass := range 3 {
		if err := srv.ReconcileEnvironmentBranchData(ctx, time.Now().UTC().Add(time.Duration(pass+1)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	again, err := service.PreviewDatabases(ctx, acct.ID, project.ID, 8)
	if err != nil || len(again) != 1 || again[0].ID == made[0].ID {
		t.Fatalf("pr-8 preview databases after reset = %+v, %v; want a new one", again, err)
	}
	if got := scoped(t, again[0].ID, "pr-8"); len(got) != 1 {
		t.Fatalf("reset pr-8 bindings to its new empty database = %+v", got)
	}
	// The reset branch is still the pull request's, so githubd keeps using it.
	if reset, err := store.ProjectEnvironmentBranchBySlug(ctx, acct.ID, project.ID, "pr-8"); err != nil || reset.PullRequestNumber != 8 {
		t.Fatalf("pr-8 after reset = %+v, %v; want pull request 8", reset, err)
	}

	// A build with nothing new to bind leaves the branch's data alone.
	if err := srv.PullRequestBranchBuild(ctx, app, "pr-8", 8); err != nil {
		t.Fatal(err)
	}
	if kept, err := store.ProjectEnvironmentBranchBySlug(ctx, acct.ID, project.ID, "pr-8"); err != nil || kept.DataState != state.EnvironmentBranchDataReady {
		t.Fatalf("pr-8 after a build = %+v, %v; want its data ready", kept, err)
	}
	// A binding production makes after the branch's data was copied reaches
	// the branch with the pull request's next build (prod 2026-11-28: a
	// migration binding never reached the release command of the PR's build).
	if _, err := bindings.Create(ctx, managedpostgres.CreateBindingRequest{AccountID: acct.ID, DatabaseID: orders.ID, AppID: app.ID,
		Scope: "production", EnvironmentKey: "REPORTS_DATABASE_URL", Access: managedpostgres.CredentialReadWrite}); err != nil {
		t.Fatal(err)
	}
	if err := srv.PullRequestBranchBuild(ctx, app, "pr-8", 8); err != nil {
		t.Fatal(err)
	}
	if reopened, err := store.ProjectEnvironmentBranchBySlug(ctx, acct.ID, project.ID, "pr-8"); err != nil || reopened.DataState != state.EnvironmentBranchDataPending || reopened.DataAttempts != 0 {
		t.Fatalf("pr-8 after a build with a new production binding = %+v, %v; want its data pending with fresh attempts", reopened, err)
	}
	for pass := range 3 {
		if err := srv.ReconcileEnvironmentBranchData(ctx, time.Now().UTC().Add(time.Duration(pass+1)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if ready, err := store.ProjectEnvironmentBranchBySlug(ctx, acct.ID, project.ID, "pr-8"); err != nil || ready.DataState != state.EnvironmentBranchDataReady {
		t.Fatalf("pr-8 after reconcile = %+v, %v; want its data ready", ready, err)
	}
	same, err := service.PreviewDatabases(ctx, acct.ID, project.ID, 8)
	if err != nil || len(same) != 1 || same[0].ID != again[0].ID {
		t.Fatalf("pr-8 preview databases after the new binding = %+v, %v; want the same empty database", same, err)
	}
	keys := map[string]bool{}
	for _, binding := range scoped(t, same[0].ID, "pr-8") {
		keys[binding.EnvironmentKey] = true
	}
	if len(keys) != 2 || !keys["DATABASE_URL"] || !keys["REPORTS_DATABASE_URL"] {
		t.Fatalf("pr-8 binds %v; want DATABASE_URL and REPORTS_DATABASE_URL", keys)
	}
}
