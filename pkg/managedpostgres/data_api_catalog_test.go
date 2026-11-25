package managedpostgres

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type catalogPreviewProvider struct {
	*fakeProvider
	resource string
}

func (p *catalogPreviewProvider) PreviewDataAPICatalog(_ context.Context, resource string) (ReadOnlyQueryResult, error) {
	p.resource = resource
	return ReadOnlyQueryResult{}, nil
}

type catalogQueryProvider struct {
	*fakeProvider
	request ReadOnlyQueryRequest
}

func (p *catalogQueryProvider) QueryReadOnly(_ context.Context, request ReadOnlyQueryRequest) (ReadOnlyQueryResult, error) {
	p.request = request
	return ReadOnlyQueryResult{}, nil
}

func TestCatalogPreviewUsesFixedOperationAndSharesQueryLimit(t *testing.T) {
	base := &fakeProvider{capabilities: testCapabilities()}
	preview := &catalogPreviewProvider{fakeProvider: base}
	query := &catalogQueryProvider{fakeProvider: base}
	for _, tc := range []struct {
		name     string
		provider Provider
	}{{"fixed endpoint", preview}, {"query fallback", query}} {
		t.Run(tc.name, func(t *testing.T) {
			registry := testRegistry(t, tc.provider, nil)
			backend, err := registry.Default("us-east-1")
			if err != nil {
				t.Fatal(err)
			}
			service := testService(t, registry, NewMemoryStore())
			db := Database{ID: "database", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, ProviderResourceID: "provider-database", State: StateReady}
			if _, err := service.PreviewDataAPICatalog(context.Background(), db); err != nil {
				t.Fatal(err)
			}
			if tc.name == "fixed endpoint" && preview.resource != "provider-database" {
				t.Fatalf("wrong provider resource: %q", preview.resource)
			}
			if tc.name == "query fallback" && (query.request.SQL != api.DataAPICatalogPreviewSQL || query.request.ProviderResourceID != "provider-database" || query.request.MaxRows != QueryMaxRows || query.request.MaxBytes != QueryMaxValueBytes) {
				t.Fatalf("unsafe query request: %+v", query.request)
			}
			service.queriesInFlight.Store(db.ID, struct{}{})
			if _, err := service.PreviewDataAPICatalog(context.Background(), db); !errors.Is(err, ErrQueryBusy) {
				t.Fatalf("busy database: %v", err)
			}
			db.State = StateDeleting
			if _, err := service.PreviewDataAPICatalog(context.Background(), db); !errors.Is(err, ErrNotReady) {
				t.Fatalf("unready database: %v", err)
			}
		})
	}
}
