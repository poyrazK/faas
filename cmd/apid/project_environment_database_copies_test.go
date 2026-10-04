// adr: 581
package main

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

type environmentClonePostgresProvider struct {
	sourceRefManagedPostgresProvider
	restores  []managedpostgres.RestoreRequest
	onRestore func() error
}

func (p *environmentClonePostgresProvider) Capabilities() managedpostgres.Capabilities {
	c := p.sourceRefManagedPostgresProvider.Capabilities()
	c.PointInTimeRestore = true
	c.MaxRestoreWindowSeconds = 3600
	return c
}

func (p *environmentClonePostgresProvider) Restore(_ context.Context, request managedpostgres.RestoreRequest) (managedpostgres.ObservedDatabase, error) {
	p.restores = append(p.restores, request)
	if p.onRestore != nil {
		if err := p.onRestore(); err != nil {
			return managedpostgres.ObservedDatabase{}, err
		}
	}
	return managedpostgres.ObservedDatabase{ProviderResourceID: "restore-" + request.ResourceID, DataResourceID: "restore-" + request.ResourceID,
		Status: managedpostgres.ProviderStatusReady, ComputeState: managedpostgres.ComputeStateActive, Spec: request.Spec,
		RestoreLineage: &managedpostgres.RestoreLineage{SourceResourceID: request.SourceResourceID, PointInTime: request.PointInTime}}, nil
}

// The provider's root datasets are immutable identities in this test backend.
func (p *environmentClonePostgresProvider) Provision(ctx context.Context, request managedpostgres.ProvisionRequest) (managedpostgres.ObservedDatabase, error) {
	observed, err := p.sourceRefManagedPostgresProvider.Provision(ctx, request)
	observed.DataResourceID = "data-" + observed.ProviderResourceID
	return observed, err
}

func TestProjectEnvironmentClonePreservesSharedDatabaseAndCleansItOnce(t *testing.T) {
	srv, store, acct, project, app := newProjectLifecycleFixture(t)
	ctx := context.Background()
	worker, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "shop-worker", WorkloadName: "worker", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	provider := &environmentClonePostgresProvider{}
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: "eu", Defaults: map[string]string{"eu": "test"}, MaxDatabasesPerAccount: 3,
		Backends: []managedpostgres.BackendConfig{{ID: "test", Driver: "test", Region: "eu", Namespace: "environment-clone"}}},
		func(string) string { return "" }, map[string]managedpostgres.Factory{
			"test": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
				return provider, nil
			},
		})
	if err != nil {
		t.Fatal(err)
	}
	databaseStore := managedpostgres.NewMemoryStore()
	service, err := managedpostgres.NewService(registry, databaseStore, managedpostgres.ServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	sink, err := newAppSecretCredentialSink(store, func() *age.X25519Recipient { return identity.Recipient() }, func() []byte { return []byte("0123456789abcdef0123456789abcdef") })
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := managedpostgres.NewBindingService(registry, databaseStore, databaseStore, sink, managedpostgres.BindingServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	srv.WithManagedPostgres(service, nil, bindings, nil, nil, nil)
	source, err := service.Create(ctx, managedpostgres.CreateRequest{AccountID: acct.ID, Name: "orders", Spec: managedpostgres.Spec{
		Region: "eu", PostgresMajor: 17, Class: managedpostgres.ClassDevelopment, Availability: managedpostgres.AvailabilitySingleZone,
		ScaleToZero: true, StorageLimitBytes: 1 << 30, RestoreWindowSeconds: 3600,
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, workload := range []state.App{app, worker} {
		if _, err := bindings.Create(ctx, managedpostgres.CreateBindingRequest{AccountID: acct.ID, DatabaseID: source.ID, AppID: workload.ID,
			Scope: "default", EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadWrite}); err != nil {
			t.Fatal(err)
		}
	}
	req, rec := projectRequest(http.MethodPost, "/v1/projects/shop/environments", "shop", []byte(`{"slug":"staging","from_environment":"production"}`))
	srv.createProjectEnvironment(rec, req, acct)
	if rec.Code != http.StatusCreated {
		t.Fatalf("clone status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(provider.restores) != 1 || provider.restores[0].SourceResourceID != source.DataResourceID || provider.restores[0].PointInTime.Nanosecond()%1000 != 0 {
		t.Fatalf("shared database was not copied once at a durable recovery time: %+v", provider.restores)
	}
	var targetDatabaseID, firstBindingID string
	for _, workload := range []state.App{app, worker} {
		secret, err := store.GetAppSecretInScope(ctx, acct.ID, workload.ID, "staging", "DATABASE_URL")
		if err != nil {
			t.Fatal(err)
		}
		binding, err := bindings.Get(ctx, acct.ID, secret.ManagedPostgresBindingID)
		if err != nil || binding.DatabaseID == source.ID || binding.Scope != "staging" || binding.AppID != workload.ID || binding.State != managedpostgres.BindingStateReady {
			t.Fatalf("isolated workload binding: %+v, %v", binding, err)
		}
		if targetDatabaseID == "" {
			targetDatabaseID, firstBindingID = binding.DatabaseID, binding.ID
		} else if binding.DatabaseID != targetDatabaseID || binding.ID == firstBindingID {
			t.Fatalf("workloads lost shared data topology or share credentials: %+v", binding)
		}
	}
	apps, snapshot, err := srv.captureProjectEnvironmentValues(ctx, acct, project, "production")
	if err != nil {
		t.Fatal(err)
	}
	plans, err := srv.planProjectEnvironmentBindingClones(ctx, acct, apps, snapshot.ValueScopes, false)
	if err != nil {
		t.Fatal(err)
	}
	_, _, cleanup, err := srv.prepareIsolatedProjectEnvironmentBindings(req, acct, project, "staging", plans)
	if err != nil || len(cleanup) != 0 || len(provider.restores) != 1 {
		t.Fatalf("retry recreated resources or adopted deletion ownership: cleanup=%d restores=%d err=%v", len(cleanup), len(provider.restores), err)
	}
	// A source edit while provider restoration is underway rejects the clone
	// and compensates only its new copy and bindings, leaving staging intact.
	provider.onRestore = func() error {
		return store.UpsertAppEnvInScope(ctx, acct.ID, app.ID, "default", "MODE", "changed-during-restore")
	}
	changedReq, changedRec := projectRequest(http.MethodPost, "/v1/projects/shop/environments", "shop", []byte(`{"slug":"changing","from_environment":"production"}`))
	srv.createProjectEnvironment(changedRec, changedReq, acct)
	if changedRec.Code != http.StatusConflict {
		t.Fatalf("source edit during data copy accepted: status=%d body=%s", changedRec.Code, changedRec.Body.String())
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, "changing"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("rejected data copy left an environment: %v", err)
	}
	for _, workload := range []state.App{app, worker} {
		rows, err := store.ListAppSecretsInScope(ctx, acct.ID, workload.ID, "changing")
		if err != nil || len(rows) != 0 {
			t.Fatalf("rejected data copy leaked managed credentials: count=%d err=%v", len(rows), err)
		}
	}
	if len(provider.restores) != 2 {
		t.Fatalf("expected one new copy for rejected preparation: restores=%d", len(provider.restores))
	}
	failedCopy, err := service.Get(ctx, acct.ID, provider.restores[1].ResourceID)
	if err != nil || failedCopy.State != managedpostgres.StateDeleted {
		t.Fatalf("rejected data copy leaked its database: %+v, %v", failedCopy, err)
	}
	retained, err := service.Get(ctx, acct.ID, targetDatabaseID)
	if err != nil || retained.State != managedpostgres.StateReady {
		t.Fatalf("compensation changed the previously created stage: %+v, %v", retained, err)
	}
	cleanupPlan, err := srv.planProjectEnvironmentManagedResourceCleanup(ctx, acct, project, "staging")
	if err != nil || len(cleanupPlan.postgres) != 2 {
		t.Fatalf("cleanup shared stage data: %+v, %v", cleanupPlan, err)
	}
	for _, item := range cleanupPlan.postgres {
		if !item.deleteDatabase || item.database.ID != targetDatabaseID {
			t.Fatalf("shared clone database lost cleanup ownership: %+v", item)
		}
	}
	if err := srv.cleanupProjectEnvironmentManagedResourcePayload(ctx, acct, projectEnvironmentCleanupResources(cleanupPlan)); err != nil {
		t.Fatal(err)
	}
	deleted, err := service.Get(ctx, acct.ID, targetDatabaseID)
	if err != nil || deleted.State != managedpostgres.StateDeleted {
		t.Fatalf("target database leaked: %+v, %v", deleted, err)
	}
	unchanged, err := service.Get(ctx, acct.ID, source.ID)
	if err != nil || unchanged.State != managedpostgres.StateReady || unchanged.ProviderResourceID != source.ProviderResourceID {
		t.Fatalf("source database changed: %+v, %v", unchanged, err)
	}
}

func TestProjectEnvironmentDatabaseCopyPointReusesCaptureAndRejectsMismatch(t *testing.T) {
	point := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	a := managedpostgres.Database{ID: "source-a", ProviderResourceID: "provider-a", DataResourceID: "provider-a/original-branch", BackendID: "test", BackendFingerprint: "fingerprint"}
	b := managedpostgres.Database{ID: "source-b", ProviderResourceID: "provider-b", BackendID: "test", BackendFingerprint: "fingerprint"}
	copies := map[string]projectEnvironmentDatabaseCopy{"source-a": {source: a, name: "copy-a"}, "source-b": {source: b, name: "copy-b"}}
	copyA := managedpostgres.Database{Name: "copy-a", RestoreSourceDatabaseID: a.ID, RestoreSourceResourceID: a.DataResourceID,
		BackendID: a.BackendID, BackendFingerprint: a.BackendFingerprint, RestorePointInTime: point, State: managedpostgres.StateReady}
	copyB := copyA
	copyB.Name, copyB.RestoreSourceDatabaseID, copyB.RestoreSourceResourceID = "copy-b", b.ID, b.ProviderResourceID
	for _, tc := range []struct {
		name string
		rows []managedpostgres.Database
		bad  bool
	}{
		{"partial retry", []managedpostgres.Database{copyA}, false},
		{"complete retry", []managedpostgres.Database{copyA, copyB}, false},
		{"different capture", []managedpostgres.Database{copyA, func() managedpostgres.Database { d := copyB; d.RestorePointInTime = point.Add(time.Second); return d }()}, true},
		{"mutable default selector", []managedpostgres.Database{func() managedpostgres.Database {
			d := copyA
			d.RestoreSourceResourceID = a.ProviderResourceID
			return d
		}()}, true},
		{"changed provider identity", []managedpostgres.Database{func() managedpostgres.Database { d := copyA; d.RestoreSourceResourceID = "different-source"; return d }()}, true},
		{"deleting target", []managedpostgres.Database{func() managedpostgres.Database { d := copyA; d.State = managedpostgres.StateDeleting; return d }()}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := projectEnvironmentDatabaseCopyPoint(copies, tc.rows)
			if tc.bad {
				if !errors.Is(err, managedpostgres.ErrConflict) {
					t.Fatalf("mismatched copy adopted: %v", err)
				}
			} else if err != nil || !got.Equal(point) {
				t.Fatalf("retry changed capture point: %v, %v", got, err)
			}
		})
	}
}

func TestProjectEnvironmentDatabaseCopiesAdoptOneLegacyCopy(t *testing.T) {
	project := state.Project{ID: "project"}
	source := managedpostgres.Database{ID: "source"}
	appA, appB := state.App{ID: "app-a"}, state.App{ID: "app-b"}
	plans := []projectEnvironmentBindingClone{{kind: "managed_postgres", app: appA, database: source}, {kind: "managed_postgres", app: appB, database: source}}
	legacyA := legacyProjectEnvironmentDatabaseCloneName(project, "staging", appA, source.ID)
	legacyB := legacyProjectEnvironmentDatabaseCloneName(project, "staging", appB, source.ID)
	for _, tc := range []struct {
		name string
		rows []managedpostgres.Database
		bad  bool
	}{
		{"adopt shared copy", []managedpostgres.Database{{Name: legacyA}}, false},
		{"reject split topology", []managedpostgres.Database{{Name: legacyA}, {Name: legacyB}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copies := map[string]projectEnvironmentDatabaseCopy{source.ID: {source: source, name: projectEnvironmentDatabaseCloneName(project, "staging", source.ID)}}
			err := adoptLegacyProjectEnvironmentDatabaseCopies(copies, tc.rows, plans, project, "staging")
			if tc.bad {
				if !errors.Is(err, managedpostgres.ErrConflict) {
					t.Fatalf("split legacy topology adopted: %v", err)
				}
			} else if err != nil || copies[source.ID].name != legacyA {
				t.Fatalf("legacy copy was not adopted: %+v, %v", copies, err)
			}
		})
	}
}
