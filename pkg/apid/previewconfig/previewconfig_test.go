package previewconfig_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/previewconfig"
	"github.com/onebox-faas/faas/pkg/state"
)

type previewFixture struct {
	store           *state.MemStore
	parent, preview state.App
}

func newPreviewFixture(t *testing.T) previewFixture {
	t.Helper()
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "preview-values@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "shop-web", WorkloadName: "web", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "pr-7-shop-web", WorkloadName: "web",
		Type: state.AppTypeApp, PreviewOfSlug: parent.Slug, PreviewPrNumber: 7})
	if err != nil {
		t.Fatal(err)
	}
	return previewFixture{store: store, parent: parent, preview: preview}
}

func (f previewFixture) values(t *testing.T) map[string]string {
	t.Helper()
	rows, err := f.store.ListAppEnvInScope(t.Context(), f.preview.AccountID, f.preview.ID, api.DefaultEnvScope)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, row := range rows {
		out[row.Key] = row.Value
	}
	return out
}

func (f previewFixture) secrets(t *testing.T) map[string]state.AppSecret {
	t.Helper()
	rows, err := f.store.ListAppSecretsInScope(t.Context(), f.preview.AccountID, f.preview.ID, api.DefaultEnvScope)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]state.AppSecret{}
	for _, row := range rows {
		out[row.Key] = row
	}
	return out
}

// ADR-20261028090000: a preview receives its production app's pr-preview
// values and secrets, keeps what was set on it directly, and never receives a
// managed credential or a default-scope production value.
func TestSyncGivesAPreviewItsProductionAppsPreviewScope(t *testing.T) {
	f := newPreviewFixture(t)
	ctx := t.Context()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(f.store.UpsertAppEnvInScope(ctx, f.parent.AccountID, f.parent.ID, api.PreviewEnvScope, "LOG_LEVEL", "debug"))
	must(f.store.UpsertAppEnvInScope(ctx, f.parent.AccountID, f.parent.ID, api.DefaultEnvScope, "PRODUCTION_ONLY", "live"))
	must(f.store.UpsertAppSecretWithClassInScope(ctx, f.parent.AccountID, f.parent.ID, api.PreviewEnvScope, "STRIPE_KEY", "", "hash-a", "", []byte("sealed-test-key")))
	must(f.store.UpsertAppSecretWithClassInScope(ctx, f.parent.AccountID, f.parent.ID, api.DefaultEnvScope, "STRIPE_LIVE_KEY", "", "hash-b", "", []byte("sealed-live-key")))
	must(f.store.PutManagedObjectStorageSecret(ctx, state.AppSecret{AccountID: f.parent.AccountID, AppID: f.parent.ID, Scope: api.PreviewEnvScope,
		Key: "BUCKET_SECRET", Ciphertext: []byte("sealed-managed"), ManagedObjectStorageCredentialID: "credential-1"}))
	must(f.store.UpsertAppEnvInScope(ctx, f.preview.AccountID, f.preview.ID, api.DefaultEnvScope, "LOG_LEVEL", "info"))
	must(f.store.UpsertAppEnvInScope(ctx, f.preview.AccountID, f.preview.ID, api.DefaultEnvScope, "SET_ON_PREVIEW", "kept"))

	result, err := previewconfig.SyncFromParent(ctx, f.store, f.preview)
	if err != nil || result.Values != 1 || result.Secrets != 1 || result.Skipped {
		t.Fatalf("sync = %+v, %v; want one value and one secret written", result, err)
	}
	values := f.values(t)
	if values["LOG_LEVEL"] != "debug" || values["SET_ON_PREVIEW"] != "kept" || values["PRODUCTION_ONLY"] != "" {
		t.Fatalf("preview values = %v; want pr-preview over the preview's own, and no production value", values)
	}
	secrets := f.secrets(t)
	if got := secrets["STRIPE_KEY"]; !bytes.Equal(got.Ciphertext, []byte("sealed-test-key")) || got.ValueHash != "hash-a" {
		t.Fatalf("preview STRIPE_KEY = %+v; want the sealed pr-preview copy", got)
	}
	if _, ok := secrets["STRIPE_LIVE_KEY"]; ok {
		t.Fatal("a default-scope production secret reached the preview")
	}
	if _, ok := secrets["BUCKET_SECRET"]; ok {
		t.Fatal("a managed credential reached the preview")
	}

	again, err := previewconfig.SyncFromParent(ctx, f.store, f.preview)
	if err != nil || again.Changed() {
		t.Fatalf("second sync = %+v, %v; want nothing to change", again, err)
	}
}

func TestSyncRefusesAppsThatAreNotItsPreview(t *testing.T) {
	f := newPreviewFixture(t)
	if _, err := previewconfig.Sync(t.Context(), f.store, f.preview, f.parent); !errors.Is(err, previewconfig.ErrNotPreview) {
		t.Fatalf("sync into the production app = %v, want ErrNotPreview", err)
	}
	if result, err := previewconfig.SyncFromParent(t.Context(), f.store, f.parent); err != nil || result.Changed() {
		t.Fatalf("sync of a production app = %+v, %v; want a no-op", result, err)
	}
	orphan := f.preview
	orphan.PreviewOfSlug = "deleted-app"
	if result, err := previewconfig.SyncFromParent(t.Context(), f.store, orphan); err != nil || result.Changed() {
		t.Fatalf("sync of a preview whose production app is gone = %+v, %v; want a no-op, not a failed build", result, err)
	}
}

// reservedEnvironmentStore reports a project environment named pr-preview,
// which predates the reservation.
type reservedEnvironmentStore struct {
	MemStore *state.MemStore
}

func (s reservedEnvironmentStore) AppBySlug(ctx context.Context, slug string) (state.App, error) {
	return s.MemStore.AppBySlug(ctx, slug)
}
func (s reservedEnvironmentStore) ProjectEnvironmentBySlug(context.Context, string, string, string) (state.ProjectEnvironment, error) {
	return state.ProjectEnvironment{Slug: api.PreviewEnvScope}, nil
}
func (s reservedEnvironmentStore) ListAppEnvInScope(ctx context.Context, accountID, appID, scope string) ([]state.AppEnv, error) {
	return s.MemStore.ListAppEnvInScope(ctx, accountID, appID, scope)
}
func (s reservedEnvironmentStore) UpsertAppEnvInScope(ctx context.Context, accountID, appID, scope, key, value string) error {
	return s.MemStore.UpsertAppEnvInScope(ctx, accountID, appID, scope, key, value)
}
func (s reservedEnvironmentStore) ListAppSecretsInScope(ctx context.Context, accountID, appID, scope string) ([]state.AppSecret, error) {
	return s.MemStore.ListAppSecretsInScope(ctx, accountID, appID, scope)
}
func (s reservedEnvironmentStore) UpsertAppSecretWithClassInScope(ctx context.Context, accountID, appID, scope, key, kid, valueHash, secretClass string, ciphertext []byte) error {
	return s.MemStore.UpsertAppSecretWithClassInScope(ctx, accountID, appID, scope, key, kid, valueHash, secretClass, ciphertext)
}

// An environment named pr-preview may hold production configuration, so its
// values never reach previews.
func TestSyncSkipsAProjectWithAnEnvironmentNamedPRPreview(t *testing.T) {
	f := newPreviewFixture(t)
	if err := f.store.UpsertAppEnvInScope(t.Context(), f.parent.AccountID, f.parent.ID, api.PreviewEnvScope, "LOG_LEVEL", "debug"); err != nil {
		t.Fatal(err)
	}
	result, err := previewconfig.SyncFromParent(t.Context(), reservedEnvironmentStore{MemStore: f.store}, f.preview)
	if err != nil || !result.Skipped || result.Changed() {
		t.Fatalf("sync = %+v, %v; want it skipped", result, err)
	}
	if values := f.values(t); values["LOG_LEVEL"] != "" {
		t.Fatalf("preview received %v despite the reserved environment", values)
	}
}
