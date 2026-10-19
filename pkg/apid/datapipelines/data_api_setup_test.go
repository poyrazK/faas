package datapipelines

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

type setupBindings struct {
	binding managedpostgres.Binding
	creates int
	ready   bool
}

func (b *setupBindings) Create(_ context.Context, r managedpostgres.CreateBindingRequest) (managedpostgres.Binding, error) {
	if r.Access != managedpostgres.CredentialReadOnly || r.EnvironmentKey != "DATABASE_URL" || r.Scope != api.DefaultEnvScope {
		return managedpostgres.Binding{}, errors.New("unsafe binding")
	}
	b.creates++
	if b.binding.ID == "" {
		b.binding = managedpostgres.Binding{ID: uuid.NewString(), AppID: r.AppID, DatabaseID: r.DatabaseID, AccountID: r.AccountID, Access: r.Access, State: managedpostgres.BindingStateReady}
	}
	return b.binding, nil
}
func (b *setupBindings) Get(_ context.Context, account, id string) (managedpostgres.Binding, error) {
	if b.binding.ID != id || b.binding.AccountID != account {
		return managedpostgres.Binding{}, managedpostgres.ErrNotFound
	}
	return b.binding, nil
}
func (b *setupBindings) ListForApp(context.Context, string, string, string) ([]managedpostgres.AppBindingInventory, error) {
	if !b.ready {
		return nil, nil
	}
	return []managedpostgres.AppBindingInventory{{Access: "auth", State: "ready"}}, nil
}

type setupNotifier struct{}

func (setupNotifier) Notify(context.Context, string, string) error { return nil }

func apiSetupFixture(t *testing.T) (*DataAPISetup, *state.MemStore, datapipeline.Setup) {
	t.Helper()
	store := state.NewMemStore()
	a, err := store.CreateAccount(t.Context(), "setup@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := store.CreateApp(t.Context(), state.App{AccountID: a.ID, Slug: "shop-auth", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateDeployment(t.Context(), state.Deployment{AppID: auth.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:" + strings.Repeat("1", 64)}); err != nil {
		t.Fatal(err)
	}
	setup := &DataAPISetup{Store: store, Bindings: &setupBindings{ready: true}, Notifier: setupNotifier{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Spool: t.TempDir(), Origin: func(_ context.Context, a state.App) string { return "https://" + a.Slug + ".example.test" }, BuildApp: func(a state.Account, slug string) (state.App, error) {
		return state.App{AccountID: a.ID, Slug: slug, Type: state.AppTypeApp}, nil
	}, ConfigureApp: func(context.Context, state.Account, state.App) error { return nil }, AdmitDeploy: func(context.Context, state.Account, state.App) error { return nil }}
	request := datapipeline.SetupRequest{Name: "shop-sync", DataAPIName: "shop-data", AuthAppID: auth.ID}
	plan, err := setup.Plan(t.Context(), a.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	plan.IntentID = uuid.NewString()
	return setup, store, datapipeline.Setup{ID: uuid.NewString(), AccountID: a.ID, DatabaseID: uuid.NewString(), Request: request, DataAPIPlan: plan}
}

func TestDataAPISetupRecoversAppBindingAndBuildIdentities(t *testing.T) {
	setup, store, job := apiSetupFixture(t)
	binding, ready, err := setup.Prepare(t.Context(), job)
	if err != nil || !ready {
		t.Fatalf("prepare %s %v %v", binding, ready, err)
	}
	job.DataAPIPlan.BindingID = binding
	if other, ready, err := setup.Prepare(t.Context(), job); err != nil || !ready || other != binding {
		t.Fatal("lost preparation reply duplicated binding", err)
	}
	a, err := store.AppByID(t.Context(), job.DataAPIPlan.AppID)
	if err != nil || a.Slug != job.Request.DataAPIName {
		t.Fatal("reserved app ID was lost", err)
	}
	admissions := 0
	setup.AdmitDeploy = func(context.Context, state.Account, state.App) error { admissions++; return nil }
	deployment, build, err := setup.Deploy(t.Context(), job)
	if err != nil {
		t.Fatal(err)
	}
	// Restart the adapter after the accepted response was lost.
	restarted := *setup
	second, secondBuild, err := restarted.Deploy(t.Context(), job)
	if err != nil || second != deployment || secondBuild != build || admissions != 1 {
		t.Fatalf("duplicate enqueue: %s %s %d %v", second, secondBuild, admissions, err)
	}
	d, err := store.DeploymentByID(t.Context(), deployment)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]string
	if err = json.Unmarshal(d.OverrideEnv, &settings); err != nil {
		t.Fatal(err)
	}
	if settings["DATA_API_READ_ONLY"] != "true" || settings["DATA_API_SCHEMAS"] != "public" || settings["DATA_API_ISSUER"] != job.DataAPIPlan.Issuer || settings["DATA_API_AUDIENCE"] != "authenticated" {
		t.Fatalf("unsafe API settings: %v", settings)
	}
	if d.Kind != state.DeploymentKindDockerfile || !strings.Contains(string(d.OverrideHealthcheck), "/healthz") {
		t.Fatal("wrong build/health path")
	}
	job.DataAPIPlan.DeploymentID = deployment
	status, endpoint, err := setup.State(t.Context(), job)
	if err != nil || status == "live" || endpoint != "" {
		t.Fatalf("premature endpoint %s %s %v", status, endpoint, err)
	}
	if err = store.MarkDeploymentLive(t.Context(), deployment); err != nil {
		t.Fatal(err)
	}
	status, endpoint, err = setup.State(t.Context(), job)
	if err != nil || status != "live" || endpoint != "https://shop-data.example.test" {
		t.Fatalf("missing ready endpoint: %s %s %v", status, endpoint, err)
	}
}

func TestDataAPISetupRejectsForeignAuthAndNameAdoption(t *testing.T) {
	setup, store, job := apiSetupFixture(t)
	foreign, err := store.CreateAccount(t.Context(), "foreign@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = setup.Plan(t.Context(), foreign.ID, job.Request); !errors.Is(err, datapipeline.ErrNotFound) {
		t.Fatalf("foreign Auth accepted: %v", err)
	}
	existing, err := store.CreateApp(t.Context(), state.App{AccountID: job.AccountID, Slug: job.Request.DataAPIName, Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = setup.Prepare(t.Context(), job); err == nil {
		t.Fatal("same-name app adopted")
	}
	if _, err = store.AppByID(t.Context(), job.DataAPIPlan.AppID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("created resource after conflict")
	}
	preserved, _ := store.AppByID(t.Context(), existing.ID)
	if preserved.ID != existing.ID {
		t.Fatal("existing app changed")
	}
	setup.Bindings.(*setupBindings).ready = false
	if _, err = setup.Plan(t.Context(), job.AccountID, job.Request); !errors.Is(err, datapipeline.ErrInvalid) {
		t.Fatalf("unconfigured Auth accepted %v", err)
	}
}

func TestDataAPISetupUsesServingAuthWhenAnotherDeploymentFails(t *testing.T) {
	setup, store, job := apiSetupFixture(t)
	if _, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: job.Request.AuthAppID, Kind: state.DeploymentKindImage, Status: state.DeployFailed, ImageDigest: "sha256:" + strings.Repeat("2", 64)}); err != nil {
		t.Fatal(err)
	}
	if _, err := setup.Plan(t.Context(), job.AccountID, job.Request); err != nil {
		t.Fatalf("a failed Auth candidate hid its serving deployment: %v", err)
	}
}

func TestDataAPISetupArchiveIsStableAcrossRepeatedSubmission(t *testing.T) {
	setup, _, job := apiSetupFixture(t)
	first, size, err := setup.source(job)
	if err != nil {
		t.Fatal(err)
	}
	second, size2, err := setup.source(job)
	if err != nil || first != second || size != size2 {
		t.Fatalf("unstable retained template %s %s %v", first, second, err)
	}
	if err = os.WriteFile(first, []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err = setup.source(job); !errors.Is(err, datapipeline.ErrConflict) {
		t.Fatalf("replaced queued source: %v", err)
	}
}

func TestDataAPISetupPostgresPreservesReservedAppIdentity(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(t.Context(), "reserved@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	request := state.App{ID: id, AccountID: account.ID, Slug: "reserved-api", Type: state.AppTypeApp}
	app, err := store.CreateAppIfUnderQuota(t.Context(), request, api.MustLimitsFor(account.Plan))
	if err != nil || app.ID != id {
		t.Fatalf("reserved ID changed: %s %s %v", id, app.ID, err)
	}
	if _, err = store.CreateAppIfUnderQuota(t.Context(), request, api.MustLimitsFor(account.Plan)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate reserved app accepted: %v", err)
	}
	rows, err := store.ListApps(t.Context(), account.ID)
	if err != nil || len(rows) != 1 {
		t.Fatal("lost reply recovery duplicated app", err)
	}
}
