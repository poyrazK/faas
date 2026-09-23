package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMemStoreOrgActivityOutboxEnvMutationAndDelivery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemStore()
	orgID, appID, actorID := uuid.New(), uuid.New(), uuid.New()
	entry := OrgActivity{
		OrgID: orgID, Kind: "env.set", ActorType: OrgActivityActorUser,
		ActorAccountID: &actorID, ActorLabel: "person@example.com",
		ResourceType: "environment_variable", ResourceID: "default:DATABASE_URL",
		ResourceLabel: "DATABASE_URL", AppID: &appID,
		SourceType: "env.set", SourceID: "request-1", Data: []byte(`{"scope":"default"}`),
	}

	bad := entry
	bad.Data = []byte(`"not an object"`)
	if _, err := store.UpsertAppEnvInScopeWithActivity(ctx, "account-1", appID.String(), "default", "SHOULD_NOT_EXIST", "secret", bad); err == nil {
		t.Fatal("upsert with invalid activity succeeded")
	}
	if rows, err := store.ListAppEnvInScope(ctx, "account-1", appID.String(), "default"); err != nil || len(rows) != 0 {
		t.Fatalf("env rows after rejected transaction = %#v, err=%v; want no mutation", rows, err)
	}

	firstID, err := store.UpsertAppEnvInScopeWithActivity(ctx, "account-1", appID.String(), "default", "DATABASE_URL", "secret", entry)
	if err != nil {
		t.Fatalf("transactional env upsert: %v", err)
	}
	duplicateID, err := store.UpsertAppEnvInScopeWithActivity(ctx, "account-1", appID.String(), "default", "DATABASE_URL", "secret", entry)
	if err != nil || duplicateID != firstID {
		t.Fatalf("duplicate upsert id = %d, err=%v; want %d", duplicateID, err, firstID)
	}
	rows, err := store.ListAppEnvInScope(ctx, "account-1", appID.String(), "default")
	if err != nil || len(rows) != 1 || rows[0].Value != "secret" {
		t.Fatalf("env rows = %#v, err=%v", rows, err)
	}

	claimed, err := store.ClaimOrgActivityOutbox(ctx, "test-worker", time.Minute)
	if err != nil || claimed.ID != firstID || claimed.Attempts != 1 {
		t.Fatalf("claim = (%+v, %v), want id=%d attempts=1", claimed, err, firstID)
	}
	delivered, err := store.DeliverOrgActivityOutbox(ctx, firstID)
	if err != nil || !delivered {
		t.Fatalf("deliver = (%v, %v), want (true, nil)", delivered, err)
	}
	delivered, err = store.DeliverOrgActivityOutbox(ctx, firstID)
	if err != nil || delivered {
		t.Fatalf("redeliver = (%v, %v), want (false, nil)", delivered, err)
	}
	activity, err := store.ListOrgActivity(ctx, OrgActivityFilter{OrgID: orgID, Limit: 10})
	if err != nil || len(activity) != 1 || activity[0].SourceID != entry.SourceID {
		t.Fatalf("activity = %#v, err=%v; want one projected event", activity, err)
	}

	deleteEntry := entry
	deleteEntry.Kind = "env.deleted"
	deleteEntry.SourceType = "env.deleted"
	deleteEntry.SourceID = "request-2"
	deleteID, err := store.DeleteAppEnvInScopeWithActivity(ctx, "account-1", appID.String(), "default", "DATABASE_URL", deleteEntry)
	if err != nil || deleteID == 0 {
		t.Fatalf("transactional env delete = (%d, %v), want a queued event", deleteID, err)
	}
	if rows, err := store.ListAppEnvInScope(ctx, "account-1", appID.String(), "default"); err != nil || len(rows) != 0 {
		t.Fatalf("env rows after delete = %#v, err=%v; want no rows", rows, err)
	}
	if _, err := store.DeleteAppEnvInScopeWithActivity(ctx, "account-1", appID.String(), "default", "DATABASE_URL", deleteEntry); !errors.Is(err, ErrNotFound) {
		t.Fatalf("duplicate delete = %v, want ErrNotFound", err)
	}

	externalEntry := deleteEntry
	externalEntry.Kind = "domain.tls_issued"
	externalEntry.ResourceType = "domain"
	externalEntry.ResourceID = "payments.example.com"
	externalEntry.ResourceLabel = "payments.example.com"
	externalEntry.SourceType = "certificate"
	externalEntry.SourceID = "payments.example.com:2030-01-01T00:00:00Z"
	externalID, err := store.EnqueueOrgActivityOutbox(ctx, externalEntry)
	if err != nil {
		t.Fatalf("enqueue external activity: %v", err)
	}
	duplicateExternalID, err := store.EnqueueOrgActivityOutbox(ctx, externalEntry)
	if err != nil || duplicateExternalID != externalID {
		t.Fatalf("duplicate external enqueue id = %d, err=%v; want %d", duplicateExternalID, err, externalID)
	}
	delivered, err = store.DeliverOrgActivityOutbox(ctx, externalID)
	if err != nil || !delivered {
		t.Fatalf("deliver external activity = (%v, %v), want (true, nil)", delivered, err)
	}
}
