package main

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// sourceRefManagedPostgresProvider is deliberately small: these tests exercise
// the deployment transport and compensation boundary, not a vendor adapter.
type sourceRefManagedPostgresProvider struct{}

func (sourceRefManagedPostgresProvider) Capabilities() managedpostgres.Capabilities {
	return managedpostgres.Capabilities{
		PostgresMajors:    []int{17},
		ServiceClasses:    []managedpostgres.ServiceClass{managedpostgres.ClassDevelopment},
		Availability:      []managedpostgres.Availability{managedpostgres.AvailabilitySingleZone},
		ScaleToZero:       true,
		PooledConnections: true,
	}
}

func (sourceRefManagedPostgresProvider) Provision(_ context.Context, request managedpostgres.ProvisionRequest) (managedpostgres.ObservedDatabase, error) {
	return managedpostgres.ObservedDatabase{
		ProviderResourceID: "provider-" + request.ResourceID,
		Status:             managedpostgres.ProviderStatusReady,
		ComputeState:       managedpostgres.ComputeStateActive,
		Spec:               request.Spec,
	}, nil
}

func (sourceRefManagedPostgresProvider) Restore(context.Context, managedpostgres.RestoreRequest) (managedpostgres.ObservedDatabase, error) {
	return managedpostgres.ObservedDatabase{}, managedpostgres.ErrUnsupported
}

func (sourceRefManagedPostgresProvider) Inspect(_ context.Context, resourceID string) (managedpostgres.ObservedDatabase, error) {
	if resourceID == "" {
		return managedpostgres.ObservedDatabase{}, managedpostgres.ErrNotFound
	}
	return managedpostgres.ObservedDatabase{
		ProviderResourceID: resourceID,
		Status:             managedpostgres.ProviderStatusReady,
		ComputeState:       managedpostgres.ComputeStateActive,
		Spec: managedpostgres.Spec{
			Region:            "eu",
			PostgresMajor:     17,
			Class:             managedpostgres.ClassDevelopment,
			Availability:      managedpostgres.AvailabilitySingleZone,
			ScaleToZero:       true,
			StorageLimitBytes: 1 << 30,
		},
	}, nil
}

func (sourceRefManagedPostgresProvider) Update(_ context.Context, request managedpostgres.UpdateRequest) (managedpostgres.ObservedDatabase, error) {
	return managedpostgres.ObservedDatabase{
		ProviderResourceID: request.ResourceID,
		Status:             managedpostgres.ProviderStatusReady,
		ComputeState:       managedpostgres.ComputeStateActive,
		Spec:               request.Spec,
	}, nil
}

func (sourceRefManagedPostgresProvider) Delete(context.Context, managedpostgres.DeleteRequest) (managedpostgres.DeleteResult, error) {
	return managedpostgres.DeleteResult{Done: true}, nil
}

func (sourceRefManagedPostgresProvider) IssueCredentials(_ context.Context, request managedpostgres.CredentialRequest) (managedpostgres.CredentialMaterial, error) {
	if request.Access != managedpostgres.CredentialReadWrite {
		return managedpostgres.CredentialMaterial{}, managedpostgres.ErrUnsupported
	}
	return managedpostgres.CredentialMaterial{
		ProviderIdentityID: "identity-" + request.IdentityKey,
		Username:           "gregale",
		Password:           "test-password",
		Database:           "gregale",
		TLSMode:            "require",
		Endpoints: []managedpostgres.Endpoint{
			{Role: managedpostgres.EndpointPooled, Host: "pool.db.example.com", Port: 5432},
			{Role: managedpostgres.EndpointDirect, Host: "direct.db.example.com", Port: 5432},
		},
	}, nil
}

func (sourceRefManagedPostgresProvider) RevokeCredentials(context.Context, managedpostgres.CredentialRequest) error {
	return nil
}

func (sourceRefManagedPostgresProvider) Usage(_ context.Context, _ string, window managedpostgres.UsageWindow) (managedpostgres.Usage, error) {
	return managedpostgres.Usage{Window: window}, nil
}

type sourceRefManagedPostgresSink struct {
	puts     map[string]string
	putCount int
	deletes  []string
}

func (s *sourceRefManagedPostgresSink) Put(_ context.Context, binding managedpostgres.Binding, _ managedpostgres.CredentialMaterial) (string, error) {
	if s.puts == nil {
		s.puts = make(map[string]string)
	}
	ref := "secret-" + binding.ID
	s.puts[binding.ID] = ref
	s.putCount++
	return ref, nil
}

func (s *sourceRefManagedPostgresSink) Delete(_ context.Context, binding managedpostgres.Binding) error {
	delete(s.puts, binding.ID)
	s.deletes = append(s.deletes, binding.ID)
	return nil
}

func configureSourceRefManagedPostgres(t *testing.T, env sourceRefTestEnv) (*managedpostgres.MemoryStore, *sourceRefManagedPostgresSink, string) {
	t.Helper()
	provider := sourceRefManagedPostgresProvider{}
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{
		DefaultRegion:          "eu",
		Defaults:               map[string]string{"eu": "test"},
		MaxDatabasesPerAccount: 3,
		ProvisioningEnabled:    true,
		Backends: []managedpostgres.BackendConfig{{
			ID: "test", Driver: "test", Region: "eu", Namespace: "source-ref",
			Settings: map[string]string{"database_name": "gregale"},
		}},
	}, func(string) string { return "" }, map[string]managedpostgres.Factory{
		"test": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
			return provider, nil
		},
	})
	if err != nil {
		t.Fatalf("managed postgres registry: %v", err)
	}
	store := managedpostgres.NewMemoryStore()
	service, err := managedpostgres.NewService(registry, store, managedpostgres.ServiceOptions{
		ProvisioningEnabled: func() bool { return true },
	})
	if err != nil {
		t.Fatalf("managed postgres service: %v", err)
	}
	database, err := service.Create(context.Background(), managedpostgres.CreateRequest{
		AccountID: env.acctID,
		Name:      "orders",
		Spec: managedpostgres.Spec{
			Region:            "eu",
			PostgresMajor:     17,
			Class:             managedpostgres.ClassDevelopment,
			Availability:      managedpostgres.AvailabilitySingleZone,
			ScaleToZero:       true,
			StorageLimitBytes: 1 << 30,
		},
	})
	if err != nil || database.State != managedpostgres.StateReady {
		t.Fatalf("managed postgres database = %+v, err=%v; want ready", database, err)
	}
	sink := &sourceRefManagedPostgresSink{puts: make(map[string]string)}
	bindingService, err := managedpostgres.NewBindingService(registry, store, store, sink, managedpostgres.BindingServiceOptions{
		ProvisioningEnabled: func() bool { return true },
	})
	if err != nil {
		t.Fatalf("managed postgres binding service: %v", err)
	}
	env.srv.WithManagedPostgres(service, nil, bindingService, nil, nil)
	return store, sink, database.ID
}

func TestSourceRef_ManagedPostgresBindingIsReadyBeforeDeployment(t *testing.T) {
	env := newSourceRefTestServer(t, api.PlanPro, "x", 7777)
	store, sink, databaseID := configureSourceRefManagedPostgres(t, env)
	env.gh.streamBody = nopReadCloser{bytes.NewReader(buildSourceRefTarGzWithManifest(t, `databases:
  - database: orders
    app: x
    scope: default
    env: DATABASE_URL
    access: read_write
`))}

	rec := env.post(t, "/v1/apps/x/deployments/source-ref", api.SourceRefDeployRequest{
		Repo: "onebox-faas/hello", Ref: "0123456789abcdef0123456789abcdef01234567",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body)
	}
	bindings, err := store.ListBindings(context.Background(), env.acctID, databaseID)
	if err != nil {
		t.Fatalf("list bindings: %v", err)
	}
	if len(bindings) != 1 {
		t.Fatalf("bindings = %d, want one", len(bindings))
	}
	binding := bindings[0]
	if binding.State != managedpostgres.BindingStateReady || binding.AppID != env.appID || binding.Scope != "default" || binding.EnvironmentKey != "DATABASE_URL" {
		t.Fatalf("binding = %+v, want ready scoped binding", binding)
	}
	if sink.puts[binding.ID] == "" {
		t.Fatalf("secret sink has no value for binding %q", binding.ID)
	}
}

func TestSourceRef_ManagedPostgresBindingRollsBackOnAcceptanceFailure(t *testing.T) {
	env := newSourceRefTestServer(t, api.PlanPro, "x", 7777)
	store, sink, databaseID := configureSourceRefManagedPostgres(t, env)
	env.gh.streamBody = nopReadCloser{bytes.NewReader(buildSourceRefTarGzWithManifest(t, `databases:
  - database: orders
    app: x
    scope: default
    env: DATABASE_URL
    access: read_write
`))}

	// The invalid tag is checked after the archive manifest has been applied;
	// this exercises the same compensation path used by a downstream enqueue
	// failure without requiring a live builder or provider.
	rec := env.post(t, "/v1/apps/x/deployments/source-ref", api.SourceRefDeployRequest{
		Repo: "onebox-faas/hello", Ref: "0123456789abcdef0123456789abcdef01234567", Tag: "not-a-valid-tag",
	})
	if rec.Code != http.StatusUnprocessableEntity || bodyCode(t, rec) != api.CodeValidation {
		t.Fatalf("status/code = %d/%q, want 422/%q; body=%s", rec.Code, bodyCode(t, rec), api.CodeValidation, rec.Body)
	}
	bindings, err := store.ListBindings(context.Background(), env.acctID, databaseID)
	if err != nil {
		t.Fatalf("list bindings: %v", err)
	}
	if len(bindings) != 0 {
		t.Fatalf("bindings = %+v, want rollback tombstone hidden", bindings)
	}
	if len(sink.puts) != 0 || len(sink.deletes) != 1 {
		t.Fatalf("secret sink puts=%v deletes=%v, want one put/delete pair", sink.puts, sink.deletes)
	}
	deleted, err := store.GetBinding(context.Background(), env.acctID, sink.deletes[0])
	if err != nil {
		t.Fatalf("get deleted binding: %v", err)
	}
	if deleted.State != managedpostgres.BindingStateDeleted {
		t.Fatalf("deleted binding = %+v, want tombstone", deleted)
	}
}

func TestSourceRef_ManagedPostgresRetryReusesReadyBinding(t *testing.T) {
	env := newSourceRefTestServer(t, api.PlanPro, "x", 7777)
	store, sink, databaseID := configureSourceRefManagedPostgres(t, env)
	archive := buildSourceRefTarGzWithManifest(t, `databases:
  - database: orders
    app: x
    scope: default
    env: DATABASE_URL
    access: read_write
`)

	for attempt := 0; attempt < 2; attempt++ {
		env.gh.streamBody = nopReadCloser{bytes.NewReader(archive)}
		rec := env.post(t, "/v1/apps/x/deployments/source-ref", api.SourceRefDeployRequest{
			Repo: "onebox-faas/hello", Ref: "0123456789abcdef0123456789abcdef01234567",
		})
		if rec.Code != http.StatusAccepted {
			t.Fatalf("attempt %d status = %d, want 202; body=%s", attempt+1, rec.Code, rec.Body)
		}
	}

	bindings, err := store.ListBindings(context.Background(), env.acctID, databaseID)
	if err != nil {
		t.Fatalf("list bindings: %v", err)
	}
	if len(bindings) != 1 || bindings[0].State != managedpostgres.BindingStateReady {
		t.Fatalf("bindings = %+v, want one ready binding", bindings)
	}
	if bindings[0].CredentialGeneration != 1 || sink.putCount != 1 {
		t.Fatalf("binding generation=%d sink puts=%d, want generation 1 and one credential write", bindings[0].CredentialGeneration, sink.putCount)
	}
}
