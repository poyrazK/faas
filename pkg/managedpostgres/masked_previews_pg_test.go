package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type maskedPreviewProvider struct {
	fakeProvider
	copies []MaskedBranchCopyRequest
	reject bool
}

func (p *maskedPreviewProvider) Provision(_ context.Context, req ProvisionRequest) (ObservedDatabase, error) {
	return ObservedDatabase{ProviderResourceID: req.ResourceID, DataResourceID: req.ResourceID, Spec: req.Spec, Status: ProviderStatusReady}, nil
}

func (p *maskedPreviewProvider) CopyMaskedBranch(_ context.Context, req MaskedBranchCopyRequest) error {
	p.copies = append(p.copies, req)
	if p.reject {
		return ErrConflict
	}
	return nil
}

func TestPostgresMaskedPreviewsPinPolicyAndBlockPublicationOnFailure(t *testing.T) {
	store, _, ctx, accountID := postgresStoreFixture(t)
	provider := &maskedPreviewProvider{fakeProvider: fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady, deleteDone: true}}
	registry, err := NewRegistry(Config{
		DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "native-a"}, MaxDatabasesPerAccount: 3,
		Backends: []BackendConfig{{ID: "native-a", Driver: "firecracker", Region: "us-east-1", Namespace: "test-native"}},
	}, func(string) string { return "" }, map[string]Factory{"firecracker": func(BackendConfig, func(string) string) (Provider, error) {
		return provider, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-3 * time.Second)
	service, err := NewService(registry, store, ServiceOptions{ProvisioningEnabled: func() bool { return true }, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	source, err := service.Create(ctx, CreateRequest{AccountID: accountID, Name: "commerce", Spec: testSpec()})
	if err != nil || source.State != StateReady {
		t.Fatalf("create source: %s, %v", source.State, err)
	}
	now = time.Now().UTC()
	if _, err := service.SetPreviews(ctx, accountID, source.ID, PreviewsCopy); err != nil {
		t.Fatal(err)
	}
	request := PreviewDatabaseRequest{AccountID: accountID, Owner: PreviewOwner{
		SourceDatabaseID: source.ID, EnvironmentID: "commerce-env", PRNumber: 1,
	}, StorageLimitBytes: api.ManagedPostgresPreviewStorageLimitBytes, Limit: 2}
	legacy, err := service.EnsurePreviewDatabase(ctx, request)
	if err != nil || legacy.IsMaskedBranch() || legacy.State != StateReady {
		t.Fatalf("create legacy preview: %s, %v", legacy.State, err)
	}
	policy := func(action string) []api.ManagedPostgresMaskedTable {
		return []api.ManagedPostgresMaskedTable{{Name: "people", Columns: []api.ManagedPostgresMaskedColumn{{Name: "email", Action: action}}}}
	}
	if _, err := service.SetMaskedPreviews(ctx, accountID, source.ID, policy("email")); !errors.Is(err, ErrPreviewReplacementRequired) {
		t.Fatalf("accepted existing full copy: %v", err)
	}
	if _, err := service.DeletePreviewDatabase(ctx, accountID, legacy.ID); err != nil {
		t.Fatal(err)
	}
	if updated, err := service.SetMaskedPreviews(ctx, accountID, source.ID, policy("email")); err != nil || updated.Previews != PreviewsMasked {
		t.Fatalf("enable masked previews: %s, %v", updated.Previews, err)
	}
	if _, err := service.EnsurePreviewDatabase(ctx, request); !errors.Is(err, ErrPreviewsOff) {
		t.Fatalf("reserved masked preview without its capability: %v", err)
	}
	request.AllowMasked = true
	first, err := service.EnsurePreviewDatabase(ctx, request)
	if err != nil || !first.IsMaskedBranch() || first.State != StateProvisioning || !first.ExpiresAt.IsZero() || len(provider.copies) != 0 {
		t.Fatalf("masked reservation: %+v, %v", first, err)
	}
	for range 2 {
		if _, err := service.SetMaskedPreviews(ctx, accountID, source.ID, policy("token")); err != nil {
			t.Fatal(err)
		}
	}
	request.AllowMasked = false
	if retry, err := service.EnsurePreviewDatabase(ctx, request); err != nil || retry.ID != first.ID {
		t.Fatalf("retry allocated another database: %s, %v", retry.ID, err)
	}
	newPR := request
	newPR.Owner.PRNumber++
	if _, err := service.EnsurePreviewDatabase(ctx, newPR); !errors.Is(err, ErrPreviewsOff) {
		t.Fatalf("reserved a new masked preview after capability revocation: %v", err)
	}
	newPR.AllowMasked = true
	second, err := service.EnsurePreviewDatabase(ctx, newPR)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		database Database
		revision int64
		action   string
	}{{first, 1, "email"}, {second, 2, "token"}} {
		stored, err := store.maskedBranchPolicy(ctx, item.database.ID)
		if err != nil || stored.previewRevision != item.revision || len(stored.tables) != 1 || stored.tables[0].Columns[0].Action != item.action {
			t.Fatalf("preview did not pin its policy: %+v, %v", stored, err)
		}
	}
	ready, err := service.Reconcile(ctx, accountID, first.ID)
	if err != nil || ready.State != StateReady || len(provider.copies) != 1 || provider.copies[0].Tables[0].Columns[0].Action != "email" {
		t.Fatalf("publish masked preview: %s, %v", ready.State, err)
	}
	if again, err := service.EnsurePreviewDatabase(ctx, request); err != nil || again.ID != first.ID || again.State != StateReady || len(provider.copies) != 1 {
		t.Fatalf("later push copied again: %+v, %v", again, err)
	}
	provider.reject = true
	if _, err := service.Reconcile(ctx, accountID, second.ID); err == nil {
		t.Fatal("failed copy was accepted")
	}
	if _, err := service.EnsurePreviewDatabase(ctx, newPR); !errors.Is(err, ErrMaskedPreviewValidationFailed) {
		t.Fatalf("failed copy did not block setup: %v", err)
	}
	if policy, err := store.maskedBranchPolicy(ctx, second.ID); err != nil || policy.validated {
		t.Fatalf("failed copy recorded evidence: %+v, %v", policy, err)
	}
	if deleted, err := service.DeletePreviewDatabase(ctx, accountID, first.ID); err != nil || deleted.State != StateDeleted {
		t.Fatalf("teardown: %s, %v", deleted.State, err)
	}
}
