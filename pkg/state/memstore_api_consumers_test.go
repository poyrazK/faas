package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStoreAPIConsumers_RoundTripAndKeyRotation(t *testing.T) {
	st := state.NewMemStore()
	ctx := context.Background()
	accountID := uuid.NewString()
	appID := uuid.NewString()

	consumer, err := st.CreateAPIConsumer(ctx, accountID, appID, "customer-42", "Acme")
	if err != nil {
		t.Fatalf("CreateAPIConsumer: %v", err)
	}
	if consumer.ID == "" || !consumer.Active() {
		t.Fatalf("new consumer = %+v, want active stable identity", consumer)
	}
	back, err := st.GetAPIConsumerByID(ctx, accountID, consumer.ID)
	if err != nil {
		t.Fatalf("GetAPIConsumerByID: %v", err)
	}
	if back.ExternalRef != "customer-42" || back.Name != "Acme" {
		t.Fatalf("round-trip consumer = %+v", back)
	}

	_, prefix1, hash1, err := api.GenerateConsumerKey()
	if err != nil {
		t.Fatal(err)
	}
	key1, err := st.CreateConsumerKeyForConsumer(ctx, accountID, consumer.ID, "primary", prefix1, hash1, []string{"read"}, nil)
	if err != nil {
		t.Fatalf("CreateConsumerKeyForConsumer primary: %v", err)
	}
	_, prefix2, hash2, err := api.GenerateConsumerKey()
	if err != nil {
		t.Fatal(err)
	}
	key2, err := st.CreateConsumerKeyForConsumer(ctx, accountID, consumer.ID, "rotated", prefix2, hash2, []string{"read", "write"}, nil)
	if err != nil {
		t.Fatalf("CreateConsumerKeyForConsumer rotated: %v", err)
	}
	if key1.ConsumerID != consumer.ID || key2.ConsumerID != consumer.ID {
		t.Fatalf("keys did not retain stable consumer ID: key1=%q key2=%q consumer=%q", key1.ConsumerID, key2.ConsumerID, consumer.ID)
	}

	list, err := st.ListAPIConsumersForApp(ctx, accountID, appID)
	if err != nil {
		t.Fatalf("ListAPIConsumersForApp: %v", err)
	}
	if len(list) != 1 || list[0].ID != consumer.ID {
		t.Fatalf("ListAPIConsumersForApp = %+v", list)
	}
}

func TestMemStoreAPIConsumers_UniquenessAndIDOR(t *testing.T) {
	st := state.NewMemStore()
	ctx := context.Background()
	accountA, accountB := uuid.NewString(), uuid.NewString()
	appA := uuid.NewString()

	consumer, err := st.CreateAPIConsumer(ctx, accountA, appA, "same-ref", "Customer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateAPIConsumer(ctx, accountA, appA, "same-ref", "Other"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate external_ref = %v, want ErrConflict", err)
	}
	if _, err := st.GetAPIConsumerByID(ctx, accountB, consumer.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account GetAPIConsumerByID = %v, want ErrNotFound", err)
	}
	if _, err := st.RevokeAPIConsumer(ctx, accountB, consumer.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account RevokeAPIConsumer = %v, want ErrNotFound", err)
	}

	revoked, err := st.RevokeAPIConsumer(ctx, accountA, consumer.ID)
	if err != nil {
		t.Fatalf("RevokeAPIConsumer: %v", err)
	}
	if revoked.Active() || revoked.RevokedAt == nil || revoked.Status != state.APIConsumerStatusRevoked {
		t.Fatalf("revoked consumer = %+v", revoked)
	}
	if _, err := st.RevokeAPIConsumer(ctx, accountA, consumer.ID); err != nil {
		t.Fatalf("idempotent RevokeAPIConsumer: %v", err)
	}

	_, prefix, hash, err := api.GenerateConsumerKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateConsumerKeyForConsumer(ctx, accountA, consumer.ID, "blocked", prefix, hash, []string{"read"}, nil); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("key for revoked consumer = %v, want ErrConflict", err)
	}
}

func TestMemStoreAPIConsumers_EmptyGuards(t *testing.T) {
	st := state.NewMemStore()
	ctx := context.Background()
	if _, err := st.CreateAPIConsumer(ctx, "", "app", "ref", "name"); err == nil {
		t.Error("CreateAPIConsumer with empty accountID: expected error")
	}
	if _, err := st.CreateAPIConsumer(ctx, "account", "app", "", "name"); err == nil {
		t.Error("CreateAPIConsumer with empty externalRef: expected error")
	}
	if _, err := st.GetAPIConsumerByID(ctx, "account", ""); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("GetAPIConsumerByID empty consumerID = %v, want ErrNotFound", err)
	}
	if _, err := st.ListAPIConsumersForApp(ctx, "", "app"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("ListAPIConsumersForApp empty accountID = %v, want ErrNotFound", err)
	}
}
