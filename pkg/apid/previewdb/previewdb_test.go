// adr: 20261102123523 — a PR preview environment gets one database per
// production database with previews on, bound to each preview with the
// production bindings' keys and access, and purged with its last preview.
package previewdb_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/previewdb"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

const account = "account-a"

// provider provisions ready databases, or provisioning ones while pending,
// and reports each database's spec back as it was asked for.
type provider struct {
	mu       sync.Mutex
	pending  bool
	specs    map[string]managedpostgres.Spec
	lineage  map[string]managedpostgres.RestoreLineage
	deleted  []string
	restores []managedpostgres.RestoreRequest
}

func (*provider) Capabilities() managedpostgres.Capabilities {
	return managedpostgres.Capabilities{
		PostgresMajors:          []int{17},
		ServiceClasses:          []managedpostgres.ServiceClass{managedpostgres.ClassDevelopment},
		Availability:            []managedpostgres.Availability{managedpostgres.AvailabilitySingleZone},
		CredentialAccess:        []managedpostgres.CredentialAccess{managedpostgres.CredentialReadWrite, managedpostgres.CredentialMigration},
		ScaleToZero:             true,
		PooledConnections:       true,
		MaxStorageBytes:         10 << 30,
		PointInTimeRestore:      true,
		RestoreUsageIsolated:    true,
		MaxRestoreWindowSeconds: 7 * 24 * 60 * 60,
	}
}

func (p *provider) observed(id string) managedpostgres.ObservedDatabase {
	status := managedpostgres.ProviderStatusReady
	if p.pending {
		status = managedpostgres.ProviderStatusPending
	}
	observed := managedpostgres.ObservedDatabase{ProviderResourceID: id, Status: status, ComputeState: managedpostgres.ComputeStateActive, Spec: p.specs[id]}
	if lineage, ok := p.lineage[id]; ok {
		observed.RestoreLineage = &lineage
	}
	return observed
}

func (p *provider) Provision(_ context.Context, request managedpostgres.ProvisionRequest) (managedpostgres.ObservedDatabase, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	id := "provider-" + request.ResourceID
	p.specs[id] = request.Spec
	return p.observed(id), nil
}

func (p *provider) Restore(_ context.Context, request managedpostgres.RestoreRequest) (managedpostgres.ObservedDatabase, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	id := "restore-" + request.ResourceID
	p.specs[id] = request.Spec
	p.lineage[id] = managedpostgres.RestoreLineage{SourceResourceID: request.SourceResourceID, PointInTime: request.PointInTime}
	p.restores = append(p.restores, request)
	return p.observed(id), nil
}

func (p *provider) Inspect(_ context.Context, id string) (managedpostgres.ObservedDatabase, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.specs[id]; !ok {
		return managedpostgres.ObservedDatabase{}, managedpostgres.ErrNotFound
	}
	return p.observed(id), nil
}

func (*provider) Update(context.Context, managedpostgres.UpdateRequest) (managedpostgres.ObservedDatabase, error) {
	return managedpostgres.ObservedDatabase{}, managedpostgres.ErrUnsupported
}

func (p *provider) Delete(_ context.Context, request managedpostgres.DeleteRequest) (managedpostgres.DeleteResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.deleted = append(p.deleted, request.ProviderResourceID)
	return managedpostgres.DeleteResult{Done: true}, nil
}

func (*provider) IssueCredentials(_ context.Context, request managedpostgres.CredentialRequest) (managedpostgres.CredentialMaterial, error) {
	return managedpostgres.CredentialMaterial{ProviderIdentityID: "identity-" + request.IdentityKey, Username: "gregale", Password: "test-password",
		Database: "gregale", TLSMode: "require", Endpoints: []managedpostgres.Endpoint{{Role: managedpostgres.EndpointPooled, Host: "pool.example.test", Port: 5432},
			{Role: managedpostgres.EndpointDirect, Host: "direct.example.test", Port: 5432}}}, nil
}

func (*provider) RevokeCredentials(context.Context, managedpostgres.CredentialRequest) error {
	return nil
}

func (*provider) Usage(_ context.Context, _ string, window managedpostgres.UsageWindow) (managedpostgres.Usage, error) {
	return managedpostgres.Usage{Window: window}, nil
}

type sink struct{}

func (sink) Put(_ context.Context, binding managedpostgres.Binding, _ managedpostgres.CredentialMaterial) (string, error) {
	return "secret-" + binding.ID, nil
}

func (sink) Delete(context.Context, managedpostgres.Binding) error { return nil }

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type fixture struct {
	manager  previewdb.Manager
	service  *managedpostgres.Service
	bindings *managedpostgres.BindingService
	provider *provider
	clock    *clock
	parent   state.App
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	p := &provider{specs: map[string]managedpostgres.Spec{}, lineage: map[string]managedpostgres.RestoreLineage{}}
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{
		DefaultRegion: "eu", Defaults: map[string]string{"eu": "test"}, MaxDatabasesPerAccount: 3, ProvisioningEnabled: true,
		Backends: []managedpostgres.BackendConfig{{ID: "test", Driver: "test", Region: "eu", Namespace: "previewdb"}},
	}, func(string) string { return "" }, map[string]managedpostgres.Factory{
		"test": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
			return p, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := managedpostgres.NewMemoryStore()
	c := &clock{now: time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)}
	service, err := managedpostgres.NewService(registry, store, managedpostgres.ServiceOptions{ProvisioningEnabled: func() bool { return true }, Now: c.Now})
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := managedpostgres.NewBindingService(registry, store, store, sink{}, managedpostgres.BindingServiceOptions{ProvisioningEnabled: func() bool { return true }, Now: c.Now})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{manager: previewdb.Manager{Databases: service, Bindings: bindings}, service: service, bindings: bindings, provider: p, clock: c,
		parent: state.App{ID: "app-shop", AccountID: account, Slug: "shop"}}
}

// database creates a ready production database bound to the parent app.
func (f fixture) database(t *testing.T, name string, previews managedpostgres.PreviewMode, bindings ...managedpostgres.CreateBindingRequest) managedpostgres.Database {
	t.Helper()
	ctx := context.Background()
	database, err := f.service.Create(ctx, managedpostgres.CreateRequest{AccountID: account, Name: name, Spec: managedpostgres.Spec{
		Region: "eu", PostgresMajor: 17, Class: managedpostgres.ClassDevelopment, Availability: managedpostgres.AvailabilitySingleZone,
		ScaleToZero: true, StorageLimitBytes: 5 << 30}})
	if err != nil || database.State != managedpostgres.StateReady {
		t.Fatalf("Create = %+v, %v", database, err)
	}
	if previews != managedpostgres.PreviewsOff {
		if database, err = f.service.SetPreviews(ctx, account, database.ID, previews); err != nil {
			t.Fatal(err)
		}
	}
	for _, request := range bindings {
		request.AccountID, request.DatabaseID = account, database.ID
		if request.AppID == "" {
			request.AppID = f.parent.ID
		}
		if _, err := f.bindings.Create(ctx, request); err != nil {
			t.Fatalf("bind %+v: %v", request, err)
		}
	}
	return database
}

func preview(id string, prNumber int, projectID string) state.App {
	return state.App{ID: id, AccountID: account, Slug: id, PreviewOfSlug: "shop", PreviewPrNumber: prNumber, ProjectID: projectID}
}

func (f fixture) previewBindings(t *testing.T, database managedpostgres.Database, appID string) map[string]managedpostgres.Binding {
	t.Helper()
	bindings, err := f.bindings.List(context.Background(), account, database.ID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]managedpostgres.Binding{}
	for _, binding := range bindings {
		if binding.AppID == appID {
			out[binding.EnvironmentKey] = binding
		}
	}
	return out
}

func TestPrepareBindsThePreviewLikeProduction(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	orders := f.database(t, "orders", managedpostgres.PreviewsEmpty,
		managedpostgres.CreateBindingRequest{Scope: api.DefaultEnvScope, EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadWrite},
		managedpostgres.CreateBindingRequest{Scope: "production", EnvironmentKey: "MIGRATION_DATABASE_URL", Access: managedpostgres.CredentialMigration},
		managedpostgres.CreateBindingRequest{Scope: "production", EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadWrite},
		managedpostgres.CreateBindingRequest{Scope: "staging", EnvironmentKey: "STAGING_DATABASE_URL", Access: managedpostgres.CredentialReadWrite})
	f.database(t, "ledger", managedpostgres.PreviewsOff,
		managedpostgres.CreateBindingRequest{Scope: api.DefaultEnvScope, EnvironmentKey: "LEDGER_URL", Access: managedpostgres.CredentialReadWrite})
	f.database(t, "unbound", managedpostgres.PreviewsEmpty)
	pr := preview("app-pr-7", 7, "project-1")

	if err := f.manager.Prepare(ctx, api.PlanPro, f.parent, pr); err != nil {
		t.Fatalf("Prepare = %v", err)
	}
	databases, err := f.service.PreviewDatabases(ctx, account, "project-1", 7)
	if err != nil || len(databases) != 1 {
		t.Fatalf("preview databases = %+v, %v; want one, for orders", databases, err)
	}
	copyOf := databases[0]
	if copyOf.Preview.SourceDatabaseID != orders.ID || copyOf.State != managedpostgres.StateReady || copyOf.Spec.StorageLimitBytes != api.ManagedPostgresPreviewStorageLimitBytes {
		t.Fatalf("preview database = %+v", copyOf)
	}
	bound := f.previewBindings(t, copyOf, pr.ID)
	if len(bound) != 2 || bound["DATABASE_URL"].Access != managedpostgres.CredentialReadWrite || bound["MIGRATION_DATABASE_URL"].Access != managedpostgres.CredentialMigration ||
		bound["DATABASE_URL"].Scope != api.DefaultEnvScope || bound["MIGRATION_DATABASE_URL"].Scope != api.DefaultEnvScope ||
		bound["DATABASE_URL"].State != managedpostgres.BindingStateReady {
		t.Fatalf("preview bindings = %+v; want production's default and production keys in the default scope", bound)
	}
	if production := f.previewBindings(t, orders, f.parent.ID); len(production) != 3 {
		t.Fatalf("production bindings changed: %+v", production)
	}

	// A second push and a second member of the environment share the database.
	if err := f.manager.Prepare(ctx, api.PlanPro, f.parent, pr); err != nil {
		t.Fatal(err)
	}
	worker := preview("app-pr-7-worker", 7, "project-1")
	if err := f.manager.Prepare(ctx, api.PlanPro, f.parent, worker); err != nil {
		t.Fatal(err)
	}
	if again, _ := f.service.PreviewDatabases(ctx, account, "project-1", 7); len(again) != 1 || again[0].ID != copyOf.ID {
		t.Fatalf("preview databases after a push and a second member = %+v", again)
	}
	if len(f.previewBindings(t, copyOf, pr.ID)) != 2 || len(f.previewBindings(t, copyOf, worker.ID)) != 2 {
		t.Fatal("each member gets its own bindings, once")
	}

	// Only the setup may bind a preview database.
	if _, err := f.bindings.Create(ctx, managedpostgres.CreateBindingRequest{AccountID: account, DatabaseID: copyOf.ID, AppID: f.parent.ID,
		Scope: api.DefaultEnvScope, EnvironmentKey: "PREVIEW_URL", Access: managedpostgres.CredentialReadWrite}); !errors.Is(err, managedpostgres.ErrPreviewDatabase) {
		t.Fatalf("customer binding to a preview database = %v", err)
	}
}

func TestPrepareWaitsForAProvisioningDatabase(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.database(t, "orders", managedpostgres.PreviewsEmpty,
		managedpostgres.CreateBindingRequest{Scope: api.DefaultEnvScope, EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadWrite})
	f.provider.pending = true
	pr := preview("app-pr-7", 7, "")
	if err := f.manager.Prepare(ctx, api.PlanPro, f.parent, pr); !errors.Is(err, previewdb.ErrPending) {
		t.Fatalf("Prepare while provisioning = %v, want ErrPending", err)
	}
	// Until the reconcilers' next attempts are due, a retry still waits.
	f.provider.pending = false
	if err := f.manager.Prepare(ctx, api.PlanPro, f.parent, pr); !errors.Is(err, previewdb.ErrPending) {
		t.Fatalf("Prepare before the next attempt is due = %v, want ErrPending", err)
	}
	f.clock.advance(time.Hour)
	if err := f.manager.Prepare(ctx, api.PlanPro, f.parent, pr); err != nil {
		t.Fatalf("Prepare once ready = %v", err)
	}
	// A legacy preview without a project is an environment of one.
	if databases, _ := f.service.PreviewDatabases(ctx, account, pr.ID, 7); len(databases) != 1 {
		t.Fatalf("legacy preview databases = %+v", databases)
	}
}

func TestPrepareRefusesPastThePlansPreviewDatabases(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.database(t, "orders", managedpostgres.PreviewsEmpty,
		managedpostgres.CreateBindingRequest{Scope: api.DefaultEnvScope, EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadWrite})
	limit := api.ManagedPostgresPreviewDatabasesMax(api.PlanHobby)
	for pr := 1; pr <= limit; pr++ {
		if err := f.manager.Prepare(ctx, api.PlanHobby, f.parent, preview(fmt.Sprintf("app-pr-%d", pr), pr, "project-1")); err != nil {
			t.Fatalf("Prepare PR %d of %d = %v", pr, limit, err)
		}
	}
	err := f.manager.Prepare(ctx, api.PlanHobby, f.parent, preview("app-pr-past", limit+1, "project-1"))
	var refused *previewdb.RefusedError
	if !errors.As(err, &refused) || !errors.Is(err, managedpostgres.ErrPreviewDatabaseLimit) || refused.Reason == "" {
		t.Fatalf("Prepare past the limit = %v, want a refusal", err)
	}
}

func TestReleasePurgesWithTheLastPreview(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.database(t, "orders", managedpostgres.PreviewsEmpty,
		managedpostgres.CreateBindingRequest{Scope: api.DefaultEnvScope, EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadWrite})
	web, worker := preview("app-pr-7", 7, "project-1"), preview("app-pr-7-worker", 7, "project-1")
	for _, member := range []state.App{web, worker} {
		if err := f.manager.Prepare(ctx, api.PlanPro, f.parent, member); err != nil {
			t.Fatal(err)
		}
	}
	databases, _ := f.service.PreviewDatabases(ctx, account, "project-1", 7)
	if len(databases) != 1 {
		t.Fatalf("preview databases = %+v", databases)
	}
	if err := f.manager.Release(ctx, web); err != nil {
		t.Fatal(err)
	}
	if left, _ := f.service.PreviewDatabases(ctx, account, "project-1", 7); len(left) != 1 || len(f.previewBindings(t, databases[0], web.ID)) != 0 ||
		len(f.previewBindings(t, databases[0], worker.ID)) != 1 {
		t.Fatalf("after the first member's teardown: databases %+v", left)
	}
	if err := f.manager.Release(ctx, worker); err != nil {
		t.Fatal(err)
	}
	if left, _ := f.service.PreviewDatabases(ctx, account, "project-1", 7); len(left) != 0 || len(f.provider.deleted) != 1 {
		t.Fatalf("after the last member's teardown: databases %+v, provider deletes %v", left, f.provider.deleted)
	}
	// A developer session is no PR preview: nothing to release.
	if err := f.manager.Release(ctx, state.App{ID: "dev", AccountID: account, PreviewOfSlug: "shop"}); err != nil {
		t.Fatal(err)
	}
}

func TestPurgeRemovesAPreviewDatabaseAndItsBindings(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.database(t, "orders", managedpostgres.PreviewsEmpty,
		managedpostgres.CreateBindingRequest{Scope: api.DefaultEnvScope, EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadWrite})
	pr := preview("app-pr-7", 7, "project-1")
	if err := f.manager.Prepare(ctx, api.PlanPro, f.parent, pr); err != nil {
		t.Fatal(err)
	}
	databases, _ := f.service.PreviewDatabases(ctx, account, "project-1", 7)
	purged, err := f.manager.Purge(ctx, account, databases[0].ID)
	if err != nil || purged.State != managedpostgres.StateDeleted || !purged.PurgeAfter.IsZero() {
		t.Fatalf("Purge = %+v, %v", purged, err)
	}
	// The next build sets it up again.
	if err := f.manager.Prepare(ctx, api.PlanPro, f.parent, pr); err != nil {
		t.Fatal(err)
	}
	if again, _ := f.service.PreviewDatabases(ctx, account, "project-1", 7); len(again) != 1 || again[0].ID == databases[0].ID {
		t.Fatalf("set up again = %+v", again)
	}
}

type previewList struct {
	apps []state.App
	err  error
}

func (l previewList) ListPreviewsForAccount(context.Context, string) ([]state.App, error) {
	return l.apps, l.err
}

func TestSweepPurgesTheDatabasesOfClosedEnvironments(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.database(t, "orders", managedpostgres.PreviewsEmpty,
		managedpostgres.CreateBindingRequest{Scope: api.DefaultEnvScope, EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadWrite})
	closed, legacy, open := preview("app-pr-7", 7, "project-1"), preview("app-pr-8", 8, ""), preview("app-pr-9", 9, "project-1")
	for _, pr := range []state.App{closed, legacy, open} {
		if err := f.manager.Prepare(ctx, api.PlanPro, f.parent, pr); err != nil {
			t.Fatal(err)
		}
	}
	page, err := f.service.PreviewDatabasePage(ctx, "", 100)
	if err != nil || len(page) != 3 {
		t.Fatalf("preview database page = %+v, %v", page, err)
	}
	// Only PR 9 is still open; PR 7's and the legacy PR 8's teardowns
	// stopped before they released their databases.
	stillOpen := previewList{apps: []state.App{open, {ID: "app-shop", AccountID: account, Slug: "shop"}}}

	if purged, err := f.manager.Sweep(ctx, stillOpen, page, f.clock.Now()); err != nil || purged != 0 {
		t.Fatalf("Sweep of databases set up just now = %d, %v; want none purged", purged, err)
	}
	if purged, err := f.manager.Sweep(ctx, previewList{err: errors.New("replica unavailable")}, page, f.clock.Now().Add(time.Minute)); err == nil || purged != 0 {
		t.Fatalf("Sweep without the account's previews = %d, %v; want an error and none purged", purged, err)
	}
	purged, err := f.manager.Sweep(ctx, stillOpen, page, f.clock.Now().Add(time.Minute))
	if err != nil || purged != 2 {
		t.Fatalf("Sweep = %d, %v; want PR 7's and PR 8's databases purged", purged, err)
	}
	for _, gone := range []state.App{closed, legacy} {
		id, prNumber, _ := previewdb.Environment(gone)
		if left, _ := f.service.PreviewDatabases(ctx, account, id, prNumber); len(left) != 0 {
			t.Fatalf("%s's databases after the sweep = %+v", gone.ID, left)
		}
	}
	if kept, _ := f.service.PreviewDatabases(ctx, account, "project-1", 9); len(kept) != 1 || len(f.previewBindings(t, kept[0], open.ID)) != 1 {
		t.Fatalf("the open PR's database after the sweep = %+v", kept)
	}
	// Another replica's sweep over the same, now stale, page deletes nothing
	// more at the provider.
	if _, err := f.manager.Sweep(ctx, stillOpen, page, f.clock.Now().Add(time.Minute)); err != nil || len(f.provider.deleted) != 2 {
		t.Fatalf("second Sweep = %v; provider deletes %v", err, f.provider.deleted)
	}
}

func TestPrepareCopiesASourceWithCopyPreviews(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	source, err := f.service.Create(ctx, managedpostgres.CreateRequest{AccountID: account, Name: "orders", Spec: managedpostgres.Spec{
		Region: "eu", PostgresMajor: 17, Class: managedpostgres.ClassDevelopment, Availability: managedpostgres.AvailabilitySingleZone,
		ScaleToZero: true, StorageLimitBytes: 5 << 30, RestoreWindowSeconds: 24 * 60 * 60}})
	if err != nil {
		t.Fatal(err)
	}
	if source, err = f.service.SetPreviews(ctx, account, source.ID, managedpostgres.PreviewsCopy); err != nil {
		t.Fatalf("SetPreviews(copy) = %v", err)
	}
	if _, err := f.bindings.Create(ctx, managedpostgres.CreateBindingRequest{AccountID: account, DatabaseID: source.ID, AppID: f.parent.ID,
		Scope: api.DefaultEnvScope, EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadWrite}); err != nil {
		t.Fatal(err)
	}
	f.database(t, "ledger", managedpostgres.PreviewsEmpty,
		managedpostgres.CreateBindingRequest{Scope: api.DefaultEnvScope, EnvironmentKey: "LEDGER_URL", Access: managedpostgres.CredentialReadWrite})
	f.clock.advance(time.Minute)
	pr := preview("app-pr-7", 7, "project-1")

	if err := f.manager.Prepare(ctx, api.PlanPro, f.parent, pr); err != nil {
		t.Fatalf("Prepare = %v", err)
	}
	databases, _ := f.service.PreviewDatabases(ctx, account, "project-1", 7)
	var copied managedpostgres.Database
	for _, database := range databases {
		if database.Preview.SourceDatabaseID == source.ID {
			copied = database
		}
	}
	if len(databases) != 2 || copied.RestoreSourceDatabaseID != source.ID || copied.State != managedpostgres.StateReady ||
		len(f.provider.restores) != 1 || f.provider.restores[0].SourceResourceID != source.ProviderResourceID {
		t.Fatalf("preview databases = %+v; restores %+v", databases, f.provider.restores)
	}
	if bound := f.previewBindings(t, copied, pr.ID); bound["DATABASE_URL"].State != managedpostgres.BindingStateReady {
		t.Fatalf("copy bindings = %+v", bound)
	}

	// Deleting the source purges its copies; the other source's empty
	// preview database stays with the PR.
	if err := f.manager.PurgeCopies(ctx, account, source.ID); err != nil {
		t.Fatal(err)
	}
	left, _ := f.service.PreviewDatabases(ctx, account, "project-1", 7)
	if len(left) != 1 || left[0].Preview.SourceDatabaseID == source.ID || len(f.previewBindings(t, copied, pr.ID)) != 0 {
		t.Fatalf("after PurgeCopies: %+v", left)
	}
}
