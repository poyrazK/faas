// adr: 583
package managedpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresCustomerDatabaseCloneVisibility(t *testing.T) {
	store, pool, ctx, accountID := postgresStoreFixture(t)
	intent := state.NewPgStore(pool)
	project, err := intent.CreateProject(ctx, state.Project{AccountID: accountID, Slug: "clone-visibility"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := intent.CreateApp(ctx, state.App{AccountID: accountID, ProjectID: project.ID, Slug: "clone-api", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	op, err := intent.CreateProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneOperation{AccountID: accountID, ProjectID: project.ID,
		SourceEnvironment: "production", TargetEnvironment: "stage", IdempotencyKey: "visibility", SourceRevisionHash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(-10 * time.Second).Truncate(time.Microsecond)
	source := postgresReadyDatabase(t, store, accountID, "production-db", at)
	input := postgresTestDatabase(accountID, "stage-db", at)
	input.RestoreSourceDatabaseID, input.RestoreSourceResourceID, input.RestorePointInTime = source.ID, source.ProviderResourceID, at.Add(time.Second)
	target, _, err := store.Reserve(ctx, input, 100)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.Claim(ctx, accountID, target.ID, "target", StateProvisioning, at, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordProviderResource(ctx, target.ID, claimed.LeaseToken, "isolated-"+target.ID, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if target, err = store.FinishProvision(ctx, target.ID, claimed.LeaseToken, at.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	// Only a future leased reservation writer may assign this owner. This
	// fixture models its committed row without opening full clone admission.
	if _, err := pool.Exec(ctx, `update managed_postgres_databases set environment_clone_operation_id=$2 where id=$1`, target.ID, op.ID); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`update managed_postgres_databases set restore_source_database_id=id where id=$1`,
		`update managed_postgres_databases set provider_resource_id=restore_source_resource_id where id=$1`,
		`update managed_postgres_databases set restore_point_in_time=null where id=$1`,
	} {
		if _, err := pool.Exec(ctx, statement, target.ID); err == nil {
			t.Fatal("owned database accepted shared identity or incomplete restore lineage")
		}
	}
	if _, _, err := store.Reserve(ctx, input, 100); !errors.Is(err, ErrConflict) {
		t.Fatalf("generic reservation adopted clone-owned placement: %v", err)
	}
	provider := &bindingProvider{fakeProvider: fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady}}
	registry := testRegistry(t, provider, nil)
	service, err := NewService(registry, store, ServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := NewBindingService(registry, store, store, newBindingCredentialSink(), BindingServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	resource := state.ProjectEnvironmentCloneResource{Kind: "managed_postgres", Name: source.ID, SourceID: source.ID, TargetID: target.ID, Status: "ready"}
	raw, _ := json.Marshal([]state.ProjectEnvironmentCloneResource{resource})
	for _, status := range []string{"pending", "capturing", "copying", "publishing", "failed", "compensating", "compensated"} {
		if _, err := pool.Exec(ctx, `update project_environment_clone_operations set status=$2, resources=$3 where id=$1`, op.ID, status, raw); err != nil {
			t.Fatal(err)
		}
		if _, err := service.Get(ctx, accountID, target.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s target exposed through customer read: %v", status, err)
		}
		if items, err := service.List(ctx, accountID); err != nil || len(items) != 1 || items[0].ID != source.ID {
			t.Fatalf("%s target exposed through customer list: %v", status, err)
		}
	}
	for _, action := range []struct {
		name string
		run  func() error
	}{
		{"create_adoption", func() error {
			_, err := service.Create(ctx, CreateRequest{AccountID: accountID, Name: target.Name, Spec: target.Spec})
			return err
		}},
		{"restore_source", func() error {
			_, err := service.Restore(ctx, RestoreDatabaseRequest{AccountID: accountID, SourceDatabaseID: target.ID, Name: "descendant", PointInTime: input.RestorePointInTime})
			return err
		}},
		{"restore_adoption", func() error {
			_, err := service.Restore(ctx, RestoreDatabaseRequest{AccountID: accountID, SourceDatabaseID: source.ID, Name: target.Name, PointInTime: input.RestorePointInTime})
			return err
		}},
		{"delete", func() error { _, err := service.Delete(ctx, accountID, target.ID); return err }},
		{"binding_create", func() error {
			_, err := bindings.Create(ctx, CreateBindingRequest{AccountID: accountID, DatabaseID: target.ID, AppID: app.ID, Scope: "stage", EnvironmentKey: "DATABASE_URL", Access: CredentialReadWrite})
			return err
		}},
		{"binding_list", func() error { _, err := bindings.List(ctx, accountID, target.ID); return err }},
		{"binding_reservation", func() error {
			_, _, err := store.ReserveBinding(ctx, Binding{ID: uuid.NewString(), AccountID: accountID, DatabaseID: target.ID, AppID: app.ID, Scope: "stage", EnvironmentKey: "DATABASE_URL", Access: CredentialReadWrite, CredentialGeneration: 1, State: BindingStateProvisioning, CreatedAt: at, UpdatedAt: at})
			return err
		}},
	} {
		if err := action.run(); !errors.Is(err, ErrNotFound) {
			t.Fatalf("pending target accepted customer %s: %v", action.name, err)
		}
	}
	if provider.restoreCalls != 0 || provider.deleteCalls != 0 || provider.issueCalls != 0 {
		t.Fatal("customer operation reached provider for a private clone")
	}
	if internal, err := store.Get(ctx, accountID, target.ID); err != nil || internal.EnvironmentCloneOperationID != op.ID || internal.State != StateReady {
		t.Fatalf("private target disappeared from internal lifecycle catalogue: %v", err)
	}
	if _, err := pool.Exec(ctx, `update project_environment_clone_operations set status='ready' where id=$1`, op.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(ctx, accountID, target.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("owner ready without its target environment exposed database: %v", err)
	}
	if _, err := intent.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: accountID, ProjectID: project.ID, Slug: "stage"}); err != nil {
		t.Fatal(err)
	}
	if visible, err := service.Get(ctx, accountID, target.ID); err != nil || visible.EnvironmentCloneOperationID != op.ID || visible.ProviderResourceID != "isolated-"+target.ID {
		t.Fatalf("ready owned target not visible with exact mapping: %v", err)
	}
	for _, fault := range []string{"source", "target", "status", "kind", "missing"} {
		bad := resource
		switch fault {
		case "source":
			bad.SourceID = uuid.NewString()
		case "target":
			bad.TargetID = uuid.NewString()
		case "status":
			bad.Status = "copying"
		case "kind":
			bad.Kind = "object_storage"
		}
		roster := []state.ProjectEnvironmentCloneResource{bad}
		if fault == "missing" {
			roster = []state.ProjectEnvironmentCloneResource{}
		}
		raw, _ := json.Marshal(roster)
		if _, err := pool.Exec(ctx, `update project_environment_clone_operations set resources=$2 where id=$1`, op.ID, raw); err != nil {
			t.Fatal(err)
		}
		if _, err := service.Get(ctx, accountID, target.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s ready receipt exposed foreign/incomplete target: %v", fault, err)
		}
	}
}

func TestMemoryCustomerDatabaseUnknownCloneOwnerIsPrivate(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	provider := &fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady}
	service := testService(t, testRegistry(t, provider, nil), store)
	database, err := service.Create(ctx, CreateRequest{AccountID: "account", Name: "owned", Spec: testSpec()})
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	database.EnvironmentCloneOperationID = uuid.NewString()
	store.databases[database.ID] = database
	store.mu.Unlock()
	if _, err := service.Get(ctx, "account", database.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown owner exposed: %v", err)
	}
	if rows, err := service.List(ctx, "account"); err != nil || len(rows) != 0 {
		t.Fatalf("unknown owner listed: %v", err)
	}
	if _, err := store.Get(ctx, "account", database.ID); err != nil {
		t.Fatalf("internal target hidden: %v", err)
	}
	if _, _, err := store.Reserve(ctx, database, 10); !errors.Is(err, ErrInvalid) {
		t.Fatalf("generic writer assigned clone owner: %v", err)
	}
}
