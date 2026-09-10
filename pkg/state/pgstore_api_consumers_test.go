package state_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreAPIConsumers_RoundTripAndIDOR(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	otherAccountID := uuid.NewString()

	consumer, err := store.CreateAPIConsumer(ctx, accountID, appID, "customer-42", "Acme")
	if err != nil {
		t.Fatalf("CreateAPIConsumer: %v", err)
	}
	if consumer.ID == "" || consumer.Status != state.APIConsumerStatusActive || !consumer.Active() {
		t.Fatalf("new consumer = %+v, want active identity", consumer)
	}
	if _, err := store.CreateAPIConsumer(ctx, accountID, appID, "customer-42", "Duplicate"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate external_ref = %v, want ErrConflict", err)
	}

	back, err := store.GetAPIConsumerByID(ctx, accountID, consumer.ID)
	if err != nil {
		t.Fatalf("GetAPIConsumerByID: %v", err)
	}
	if back.ExternalRef != consumer.ExternalRef || back.Name != consumer.Name {
		t.Fatalf("round-trip consumer = %+v, want %+v", back, consumer)
	}
	if _, err := store.GetAPIConsumerByID(ctx, otherAccountID, consumer.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account GetAPIConsumerByID = %v, want ErrNotFound", err)
	}

	list, err := store.ListAPIConsumersForApp(ctx, accountID, appID)
	if err != nil {
		t.Fatalf("ListAPIConsumersForApp: %v", err)
	}
	if len(list) != 1 || list[0].ID != consumer.ID {
		t.Fatalf("ListAPIConsumersForApp = %+v", list)
	}

	_, prefix, hash, err := api.GenerateConsumerKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.CreateConsumerKeyForConsumer(ctx, accountID, consumer.ID, "primary", prefix, hash, []string{"read"}, nil)
	if err != nil {
		t.Fatalf("CreateConsumerKeyForConsumer: %v", err)
	}
	if key.ConsumerID != consumer.ID || key.AccountID != accountID || key.AppID != appID {
		t.Fatalf("key linkage = %+v, want consumer=%s account=%s app=%s", key, consumer.ID, accountID, appID)
	}
	keyBack, err := store.GetConsumerKeyByID(ctx, accountID, key.ID)
	if err != nil {
		t.Fatalf("GetConsumerKeyByID: %v", err)
	}
	if keyBack.ConsumerID != consumer.ID {
		t.Fatalf("GetConsumerKeyByID ConsumerID = %q, want %q", keyBack.ConsumerID, consumer.ID)
	}

	_, prefix2, hash2, err := api.GenerateConsumerKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateConsumerKeyForConsumer(ctx, accountID, consumer.ID, "primary", prefix2, hash2, []string{"read"}, nil); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate key name = %v, want ErrConflict", err)
	}
	if _, err := store.CreateConsumerKeyForConsumer(ctx, otherAccountID, consumer.ID, "idor", prefix2, hash2, []string{"read"}, nil); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account key creation = %v, want ErrNotFound", err)
	}

	revoked, err := store.RevokeAPIConsumer(ctx, accountID, consumer.ID)
	if err != nil {
		t.Fatalf("RevokeAPIConsumer: %v", err)
	}
	if revoked.Active() || revoked.Status != state.APIConsumerStatusRevoked || revoked.RevokedAt == nil {
		t.Fatalf("revoked consumer = %+v", revoked)
	}
	if _, err := store.RevokeAPIConsumer(ctx, accountID, consumer.ID); err != nil {
		t.Fatalf("idempotent RevokeAPIConsumer: %v", err)
	}
	if _, err := store.CreateConsumerKeyForConsumer(ctx, accountID, consumer.ID, "after-revoke", prefix2, hash2, []string{"read"}, nil); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("key for revoked consumer = %v, want ErrConflict", err)
	}
}

func TestPgStoreAPIConsumers_EmptyGuards(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	if _, err := store.CreateAPIConsumer(ctx, "", "app", "ref", "name"); err == nil {
		t.Error("CreateAPIConsumer empty accountID: expected error")
	}
	if _, err := store.CreateAPIConsumer(ctx, "account", "app", "", "name"); err == nil {
		t.Error("CreateAPIConsumer empty externalRef: expected error")
	}
	if _, err := store.GetAPIConsumerByID(ctx, "account", ""); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("GetAPIConsumerByID empty consumerID = %v, want ErrNotFound", err)
	}
	if _, err := store.ListAPIConsumersForApp(ctx, "", "app"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("ListAPIConsumersForApp empty accountID = %v, want ErrNotFound", err)
	}
	if _, err := store.RevokeAPIConsumer(ctx, "", "consumer"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("RevokeAPIConsumer empty accountID = %v, want ErrNotFound", err)
	}
	if _, err := store.CreateConsumerKeyForConsumer(ctx, "", "consumer", "name", "prefix", make([]byte, 32), []string{"read"}, nil); err == nil {
		t.Error("CreateConsumerKeyForConsumer empty accountID: expected error")
	}
}
