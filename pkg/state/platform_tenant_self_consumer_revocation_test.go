package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type platformTenantSelfConsumerRevocationFixture interface {
	state.PlatformTenantSelfConsumerRevocationStore
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreatePlatformTenant(context.Context, string, string, string, int) (state.PlatformTenant, bool, error)
	SetPlatformTenantStatus(context.Context, string, string, string) (state.PlatformTenant, error)
	CreateApp(context.Context, state.App) (state.App, error)
	CreateAPIConsumer(context.Context, string, string, string, string) (state.APIConsumer, error)
	LinkPlatformTenantConsumer(context.Context, string, string, string) (state.APIConsumer, error)
	CreateConsumerKeyForConsumer(context.Context, string, string, string, string, []byte, []string, *time.Time) (state.ConsumerKey, error)
	GetAPIConsumerByID(context.Context, string, string) (state.APIConsumer, error)
	ListConsumerKeysForApp(context.Context, string, string) ([]state.ConsumerKey, error)
}

func TestMemPlatformTenantSelfConsumerRevocation(t *testing.T) {
	testPlatformTenantSelfConsumerRevocation(t, state.NewMemStore(), context.Background())
}

func TestPgPlatformTenantSelfConsumerRevocation(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	testPlatformTenantSelfConsumerRevocation(t, store, ctx)
}

func testPlatformTenantSelfConsumerRevocation(t *testing.T, store platformTenantSelfConsumerRevocationFixture, ctx context.Context) {
	t.Helper()
	account, err := store.CreateAccount(ctx, "self-consumer-revoke-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "self-consumer-revoke", "Self consumer revoke", 100)
	if err != nil {
		t.Fatal(err)
	}
	otherTenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "other-consumer-revoke", "Other consumer revoke", 100)
	if err != nil {
		t.Fatal(err)
	}

	makeConsumer := func(label, tenantID string, linked bool) (state.App, state.APIConsumer) {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "self-consumer-revoke-" + uuid.NewString()[:8],
			Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1})
		if err != nil {
			t.Fatal(err)
		}
		consumer, err := store.CreateAPIConsumer(ctx, account.ID, app.ID, label+"-"+uuid.NewString()[:8], label)
		if err != nil {
			t.Fatal(err)
		}
		if linked {
			consumer, err = store.LinkPlatformTenantConsumer(ctx, account.ID, tenantID, consumer.ID)
			if err != nil {
				t.Fatal(err)
			}
		}
		return app, consumer
	}
	appA, consumerA := makeConsumer("customer-a", tenant.ID, true)
	appB, consumerB := makeConsumer("customer-b", tenant.ID, true)
	_, foreignConsumer := makeConsumer("other-tenant-customer", otherTenant.ID, true)
	_, unlinkedConsumer := makeConsumer("unlinked-customer", "", false)

	makeKey := func(consumer state.APIConsumer, name string) state.ConsumerKey {
		t.Helper()
		key, err := store.CreateConsumerKeyForConsumer(ctx, account.ID, consumer.ID, name,
			"prefix-"+uuid.NewString()[:8], api.HashAPIKey("secret-"+uuid.NewString()), []string{"read"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return key
	}
	keyA := makeKey(consumerA, "customer-a-key")
	keyB := makeKey(consumerB, "customer-b-key")
	ids := []string{consumerB.ID, consumerA.ID}
	input := state.RevokePlatformTenantSelfConsumersParams{AccountID: account.ID, TenantID: tenant.ID, ConsumerIDs: ids}

	if _, err := store.RevokePlatformTenantSelfConsumers(ctx, state.RevokePlatformTenantSelfConsumersParams{
		AccountID: account.ID, TenantID: tenant.ID, ConsumerIDs: []string{consumerA.ID, foreignConsumer.ID},
	}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("mixed-tenant batch error=%v, want not found", err)
	}
	if _, err := store.RevokePlatformTenantSelfConsumers(ctx, state.RevokePlatformTenantSelfConsumersParams{
		AccountID: account.ID, TenantID: tenant.ID, ConsumerIDs: []string{consumerA.ID, unlinkedConsumer.ID},
	}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unlinked batch error=%v, want not found", err)
	}
	stillActive, err := store.GetAPIConsumerByID(ctx, account.ID, consumerA.ID)
	if err != nil || !stillActive.Active() {
		t.Fatalf("failed batch partially revoked consumer: %+v err=%v", stillActive, err)
	}
	for _, item := range []struct {
		app   state.App
		keyID string
	}{{appA, keyA.ID}, {appB, keyB.ID}} {
		keys, err := store.ListConsumerKeysForApp(ctx, account.ID, item.app.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) != 1 || keys[0].ID != item.keyID || keys[0].RevokedAt != nil {
			t.Fatalf("failed batch changed key %s: %+v", item.keyID, keys)
		}
	}

	if _, err := store.RevokePlatformTenantSelfConsumers(ctx, state.RevokePlatformTenantSelfConsumersParams{
		AccountID: account.ID, TenantID: tenant.ID, ConsumerIDs: []string{consumerA.ID, consumerA.ID},
	}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("duplicate ID error=%v, want invalid argument", err)
	}
	tooMany := make([]string, state.MaxPlatformTenantSelfConsumerRevocationBatch+1)
	for i := range tooMany {
		tooMany[i] = uuid.NewString()
	}
	if _, err := store.RevokePlatformTenantSelfConsumers(ctx, state.RevokePlatformTenantSelfConsumersParams{
		AccountID: account.ID, TenantID: tenant.ID, ConsumerIDs: tooMany,
	}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("oversized batch error=%v, want invalid argument", err)
	}

	if _, err := store.SetPlatformTenantStatus(ctx, account.ID, tenant.ID, state.PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	result, err := store.RevokePlatformTenantSelfConsumers(ctx, input)
	if err != nil || !result.Changed || result.RevokedKeys != 2 || len(result.Consumers) != 2 {
		t.Fatalf("revoke result=%+v err=%v", result, err)
	}
	if result.Consumers[0].ID > result.Consumers[1].ID {
		t.Fatalf("consumer results not ordered by ID: %+v", result.Consumers)
	}
	for _, consumer := range result.Consumers {
		if consumer.Status != state.APIConsumerStatusRevoked || consumer.RevokedAt == nil {
			t.Errorf("consumer not revoked: %+v", consumer)
		}
	}
	for _, item := range []struct {
		app   state.App
		keyID string
	}{{appA, keyA.ID}, {appB, keyB.ID}} {
		keys, err := store.ListConsumerKeysForApp(ctx, account.ID, item.app.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) != 1 || keys[0].ID != item.keyID || keys[0].RevokedAt == nil {
			t.Fatalf("key %s not revoked: %+v", item.keyID, keys)
		}
	}

	replay, err := store.RevokePlatformTenantSelfConsumers(ctx, input)
	if err != nil || replay.Changed || replay.RevokedKeys != 0 || len(replay.Consumers) != 2 {
		t.Fatalf("replay result=%+v err=%v", replay, err)
	}
}

func TestValidatePlatformTenantSelfConsumerRevocationBatch(t *testing.T) {
	base := state.RevokePlatformTenantSelfConsumersParams{AccountID: uuid.NewString(), TenantID: uuid.NewString(),
		ConsumerIDs: []string{uuid.NewString()}}
	for _, tc := range []struct {
		name string
		edit func(*state.RevokePlatformTenantSelfConsumersParams)
	}{
		{name: "empty", edit: func(in *state.RevokePlatformTenantSelfConsumersParams) { in.ConsumerIDs = nil }},
		{name: "invalid ID", edit: func(in *state.RevokePlatformTenantSelfConsumersParams) { in.ConsumerIDs[0] = "not-a-uuid" }},
		{name: "missing tenant", edit: func(in *state.RevokePlatformTenantSelfConsumersParams) { in.TenantID = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			in.ConsumerIDs = append([]string(nil), base.ConsumerIDs...)
			tc.edit(&in)
			if _, err := state.NewMemStore().RevokePlatformTenantSelfConsumers(context.Background(), in); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("error=%v, want invalid argument", err)
			}
		})
	}
}
