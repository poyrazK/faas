package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type credentialFixtureStore interface {
	state.PlatformTenantCredentialStore
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreateApp(context.Context, state.App) (state.App, error)
	CreateAPIConsumer(context.Context, string, string, string, string) (state.APIConsumer, error)
	CreatePlatformTenant(context.Context, string, string, string, int) (state.PlatformTenant, bool, error)
	LinkPlatformTenantConsumer(context.Context, string, string, string) (state.APIConsumer, error)
}

func TestMemPlatformTenantCredentialRotation(t *testing.T) {
	testPlatformTenantCredentialRotation(t, state.NewMemStore())
}

func TestPgPlatformTenantCredentialRotation(t *testing.T) {
	store, _, _ := pgStoreWithPool(t)
	testPlatformTenantCredentialRotation(t, store)
}

func testPlatformTenantCredentialRotation(t *testing.T, store credentialFixtureStore) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "credential-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "credentials-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := store.CreateAPIConsumer(ctx, account.ID, app.ID, "customer", "Customer")
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "customer", "Customer", 250)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkPlatformTenantConsumer(ctx, account.ID, tenant.ID, consumer.ID); err != nil {
		t.Fatal(err)
	}
	_, prefix, hash, err := api.GenerateConsumerKey()
	if err != nil {
		t.Fatal(err)
	}
	in := state.ApplyPlatformTenantCredentialsParams{AccountID: account.ID, TenantID: tenant.ID,
		AppLimit: 1, AccountLimit: 1, Keys: []state.PlatformTenantCredentialIntent{{ConsumerID: consumer.ID,
			Name: "v1", Prefix: prefix, Hash: hash, Scopes: []string{"read"}}}, DryRun: true}
	preview, err := store.ApplyPlatformTenantCredentials(ctx, in)
	if err != nil || len(preview.Keys) != 1 || preview.Keys[0].Action != "create" {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	if rows, err := store.ListPlatformTenantCredentials(ctx, account.ID, tenant.ID, 100, 0); err != nil || len(rows) != 0 {
		t.Fatalf("dry run wrote keys: %+v, %v", rows, err)
	}
	in.DryRun = false
	created, err := store.ApplyPlatformTenantCredentials(ctx, in)
	if err != nil || created.Keys[0].Key.ID == "" {
		t.Fatalf("create = %+v, %v", created, err)
	}
	replayed, err := store.ApplyPlatformTenantCredentials(ctx, in)
	if err != nil || replayed.Keys[0].Action != "unchanged" || replayed.Keys[0].Key.ID != created.Keys[0].Key.ID {
		t.Fatalf("replay = %+v, %v", replayed, err)
	}
	_, prefix2, hash2, err := api.GenerateConsumerKey()
	if err != nil {
		t.Fatal(err)
	}
	rotation := in
	rotation.Keys = []state.PlatformTenantCredentialIntent{{ConsumerID: consumer.ID, Name: "v2", Prefix: prefix2, Hash: hash2, Scopes: []string{"read"}}}
	rotation.RevokeKeyIDs = []string{created.Keys[0].Key.ID}
	rotated, err := store.ApplyPlatformTenantCredentials(ctx, rotation)
	if err != nil || len(rotated.Keys) != 2 || rotated.Keys[0].Action != "revoke" || rotated.Keys[1].Action != "create" {
		t.Fatalf("rotation = %+v, %v", rotated, err)
	}
	rotationReplay, err := store.ApplyPlatformTenantCredentials(ctx, rotation)
	if err != nil || rotationReplay.Keys[0].Action != "unchanged" || rotationReplay.Keys[1].Action != "unchanged" {
		t.Fatalf("rotation replay = %+v, %v", rotationReplay, err)
	}
	rows, err := store.ListPlatformTenantCredentials(ctx, account.ID, tenant.ID, 100, 0)
	if err != nil || len(rows) != 2 {
		t.Fatalf("list = %+v, %v", rows, err)
	}
	conflict := rotation
	conflict.RevokeKeyIDs = nil
	conflict.Keys[0].Hash = hash
	if _, err := store.ApplyPlatformTenantCredentials(ctx, conflict); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed hash = %v", err)
	}
	_, prefix3, hash3, err := api.GenerateConsumerKey()
	if err != nil {
		t.Fatal(err)
	}
	tooMany := in
	tooMany.Keys = []state.PlatformTenantCredentialIntent{{ConsumerID: consumer.ID, Name: "v3", Prefix: prefix3, Hash: hash3, Scopes: []string{"read"}}}
	var quota *state.PlatformTenantCredentialQuotaError
	if _, err := store.ApplyPlatformTenantCredentials(ctx, tooMany); !errors.As(err, &quota) {
		t.Fatalf("quota = %v", err)
	}
	rows, err = store.ListPlatformTenantCredentials(ctx, account.ID, tenant.ID, 100, 0)
	if err != nil || len(rows) != 2 {
		t.Fatalf("quota wrote key: %+v, %v", rows, err)
	}
	if _, err := store.ListPlatformTenantCredentials(ctx, uuid.NewString(), tenant.ID, 100, 0); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign list = %v", err)
	}
}
