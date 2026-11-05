// adr: 20261102123523 — a production database's previews setting gives each
// PR preview its own database, bound like production; the preview's teardown
// purges it, a customer's delete purges it at once, and only the setup may
// bind it.
package apidserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/previewdb"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

type previewDatabaseSink struct{}

func (previewDatabaseSink) Put(_ context.Context, binding managedpostgres.Binding, _ managedpostgres.CredentialMaterial) (string, error) {
	return "secret-" + binding.ID, nil
}

func (previewDatabaseSink) Delete(context.Context, managedpostgres.Binding) error { return nil }

// previewDatabaseFixture wires managed PostgreSQL into e and creates the
// production app shop bound to orders as DATABASE_URL and, for its release
// command, MIGRATION_DATABASE_URL.
func previewDatabaseFixture(t *testing.T, e apidserver.TestEnv) (*managedpostgres.Service, *managedpostgres.BindingService, state.App, managedpostgres.Database) {
	t.Helper()
	ctx := t.Context()
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{
		DefaultRegion: "eu", Defaults: map[string]string{"eu": "test"}, MaxDatabasesPerAccount: 3, ProvisioningEnabled: true,
		Backends: []managedpostgres.BackendConfig{{ID: "test", Driver: "test", Region: "eu", Namespace: "preview-databases"}},
	}, func(string) string { return "" }, map[string]managedpostgres.Factory{
		"test": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
			return apidserver.SourceRefManagedPostgresProvider{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := managedpostgres.NewMemoryStore()
	service, err := managedpostgres.NewService(registry, store, managedpostgres.ServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := managedpostgres.NewBindingService(registry, store, store, previewDatabaseSink{}, managedpostgres.BindingServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	(*e.S()).WithManagedPostgres(service, nil, bindings, nil, nil, nil)
	parent, err := (*e.Store()).CreateAppIfUnderQuota(ctx, state.App{AccountID: e.Acct().ID, Slug: "shop", Type: "stateless", Runtime: "node22",
		RAMMB: 512, MaxConcurrency: 2, IdleTimeoutS: 45, Status: state.AppActive, WorkloadClass: state.WorkloadClassHTTP}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	orders, err := service.Create(ctx, managedpostgres.CreateRequest{AccountID: e.Acct().ID, Name: "orders", Spec: managedpostgres.Spec{
		Region: "eu", PostgresMajor: 17, Class: managedpostgres.ClassDevelopment, Availability: managedpostgres.AvailabilitySingleZone,
		ScaleToZero: true, StorageLimitBytes: 1 << 30}})
	if err != nil || orders.State != managedpostgres.StateReady {
		t.Fatalf("create orders = %+v, %v", orders, err)
	}
	for key, access := range map[string]managedpostgres.CredentialAccess{"DATABASE_URL": managedpostgres.CredentialReadWrite, "MIGRATION_DATABASE_URL": managedpostgres.CredentialMigration} {
		if _, err := bindings.Create(ctx, managedpostgres.CreateBindingRequest{AccountID: e.Acct().ID, DatabaseID: orders.ID, AppID: parent.ID,
			Scope: api.DefaultEnvScope, EnvironmentKey: key, Access: access}); err != nil {
			t.Fatalf("bind %s: %v", key, err)
		}
	}
	return service, bindings, parent, orders
}

func createPreview(t *testing.T, e apidserver.TestEnv, prNumber int) state.App {
	t.Helper()
	rec := e.Do(t, http.MethodPost, "/v1/apps/shop/previews", api.CreatePreviewRequest{PRNumber: prNumber}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create preview: %d %s", rec.Code, rec.Body)
	}
	var created api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	preview, err := (*e.Store()).AppBySlug(t.Context(), created.Slug)
	if err != nil {
		t.Fatal(err)
	}
	return preview
}

func TestPreviewDatabasesFollowTheirPreview(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx := t.Context()
	service, bindings, parent, orders := previewDatabaseFixture(t, e)
	acct := *e.Acct()

	rec := e.Do(t, http.MethodPut, "/v1/postgres/databases/"+orders.ID+"/previews", api.SetManagedPostgresPreviewsRequest{Previews: "copy"}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("copy of a database without a restore window: %d %s", rec.Code, rec.Body)
	}
	rec = e.Do(t, http.MethodPut, "/v1/postgres/databases/"+orders.ID+"/previews", api.SetManagedPostgresPreviewsRequest{Previews: "empty"}, nil)
	var view api.ManagedPostgresDatabase
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &view) != nil || view.Previews != "empty" || view.Preview != nil {
		t.Fatalf("set previews: %d %s", rec.Code, rec.Body)
	}

	preview := createPreview(t, e, 7)
	databases, err := service.PreviewDatabases(ctx, acct.ID, preview.ID, 7)
	if err != nil || len(databases) != 1 || databases[0].State != managedpostgres.StateReady || databases[0].Preview.SourceDatabaseID != orders.ID {
		t.Fatalf("preview databases = %+v, %v", databases, err)
	}
	previewDB := databases[0]
	bound, err := bindings.List(ctx, acct.ID, previewDB.ID)
	keys := map[string]managedpostgres.CredentialAccess{}
	for _, binding := range bound {
		if binding.AppID == preview.ID && binding.Scope == api.DefaultEnvScope && binding.State == managedpostgres.BindingStateReady {
			keys[binding.EnvironmentKey] = binding.Access
		}
	}
	if err != nil || len(keys) != 2 || keys["DATABASE_URL"] != managedpostgres.CredentialReadWrite || keys["MIGRATION_DATABASE_URL"] != managedpostgres.CredentialMigration {
		t.Fatalf("preview bindings = %+v, %v", bound, err)
	}

	rec = e.Do(t, http.MethodGet, "/v1/postgres/databases/"+previewDB.ID, nil, nil)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &view) != nil || view.Preview == nil || view.Preview.SourceDatabaseID != orders.ID || view.Preview.PRNumber != 7 {
		t.Fatalf("preview database view: %d %s", rec.Code, rec.Body)
	}
	rec = e.Do(t, http.MethodPost, "/v1/postgres/databases/"+previewDB.ID+"/bindings", api.CreateManagedPostgresBindingRequest{
		AppID: parent.ID, Scope: api.DefaultEnvScope, EnvironmentKey: "PREVIEW_URL", Access: "read_write"}, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("binding a preview database by hand: %d %s", rec.Code, rec.Body)
	}

	// A build of the same preview reuses its database.
	if err := (*e.S()).PrepareBuildPreviewDatabases(ctx, acct, acct.Plan, preview); err != nil {
		t.Fatalf("build setup = %v", err)
	}
	if again, _ := service.PreviewDatabases(ctx, acct.ID, preview.ID, 7); len(again) != 1 || again[0].ID != previewDB.ID {
		t.Fatalf("preview databases after a build = %+v", again)
	}

	rec = e.Do(t, http.MethodPost, "/v1/preview/"+preview.Slug+"/destroy", nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("destroy preview: %d %s", rec.Code, rec.Body)
	}
	if left, _ := service.PreviewDatabases(ctx, acct.ID, preview.ID, 7); len(left) != 0 {
		t.Fatalf("preview databases after the teardown = %+v", left)
	}
	if production, err := bindings.List(ctx, acct.ID, orders.ID); err != nil || len(production) != 2 {
		t.Fatalf("production bindings after the teardown = %+v, %v", production, err)
	}

	// A customer's delete of a preview database purges it at once.
	second := createPreview(t, e, 8)
	databases, _ = service.PreviewDatabases(ctx, acct.ID, second.ID, 8)
	if len(databases) != 1 {
		t.Fatalf("second preview's databases = %+v", databases)
	}
	rec = e.Do(t, http.MethodDelete, "/v1/postgres/databases/"+databases[0].ID, nil, nil)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &view) != nil || view.State != string(managedpostgres.StateDeleted) || view.RestorableUntil != "" {
		t.Fatalf("delete a preview database: %d %s", rec.Code, rec.Body)
	}
}

func TestPreviewDatabasesBuildStatus(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code codes.Code
	}{
		{nil, codes.OK},
		{previewdb.ErrPending, codes.Unavailable},
		{&previewdb.RefusedError{Reason: "the plan's PR preview databases are all in use", Err: managedpostgres.ErrPreviewDatabaseLimit}, codes.ResourceExhausted},
		{errors.New("provider timeout"), codes.Unavailable},
	} {
		got := apidserver.PreviewDatabasesStatus(tc.err)
		if status.Code(got) != tc.code {
			t.Fatalf("%v → %v, want %s", tc.err, got, tc.code)
		}
	}
	// githubd writes a ResourceExhausted message to the pull request.
	refused := apidserver.PreviewDatabasesStatus(&previewdb.RefusedError{Reason: "the plan's PR preview databases are all in use", Err: managedpostgres.ErrPreviewDatabaseLimit})
	if status.Convert(refused).Message() != "EnqueueBuild: the plan's PR preview databases are all in use" {
		t.Fatalf("refusal message = %q", status.Convert(refused).Message())
	}
}

// A teardown that stopped before it released its database leaves the
// database to the sweep, which keeps the open PR's.
func TestPreviewDatabaseSweepFinishesAStoppedTeardown(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx := t.Context()
	service, _, _, orders := previewDatabaseFixture(t, e)
	acct := *e.Acct()
	if _, err := service.SetPreviews(ctx, acct.ID, orders.ID, managedpostgres.PreviewsEmpty); err != nil {
		t.Fatal(err)
	}
	stopped, open := createPreview(t, e, 7), createPreview(t, e, 8)
	if _, err := (*e.Store()).SoftDeleteAppCascade(ctx, stopped.ID); err != nil {
		t.Fatal(err)
	}
	if purged, err := (*e.S()).SweepPreviewDatabases(ctx, time.Now().Add(-time.Hour)); err != nil || purged != 0 {
		t.Fatalf("sweep of databases set up within the grace = %d, %v", purged, err)
	}
	if purged, err := (*e.S()).SweepPreviewDatabases(ctx, time.Now().Add(time.Hour)); err != nil || purged != 1 {
		t.Fatalf("sweep = %d, %v; want the stopped teardown's database purged", purged, err)
	}
	for _, tc := range []struct {
		app  state.App
		want int
	}{{stopped, 0}, {open, 1}} {
		environmentID, prNumber, _ := previewdb.Environment(tc.app)
		want := tc.want
		if databases, err := service.PreviewDatabases(ctx, acct.ID, environmentID, prNumber); err != nil || len(databases) != want {
			t.Fatalf("PR %d's databases after the sweep = %+v, %v; want %d", prNumber, databases, err, want)
		}
	}
}
