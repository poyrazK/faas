package state

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMemStoreOrgAPIKeyMutationsAreAtomicWithActivity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemStore()
	orgID, actorID := uuid.New(), uuid.New()
	accountID := uuid.NewString()
	newActivity := func(kind, sourceID string, data []byte) OrgActivity {
		return OrgActivity{
			OrgID: orgID, Kind: kind, ActorType: OrgActivityActorUser,
			ActorAccountID: &actorID, ActorLabel: "owner@example.com",
			ResourceType: "api_key", ResourceLabel: "API key", SourceType: kind,
			SourceID: sourceID, Data: data,
		}
	}
	const plaintext = "gk_live_this-is-a-secret"
	initialHash := []byte("stored-hash-not-for-timeline")
	expiry := time.Now().UTC().Add(24 * time.Hour)
	badCreate := newActivity("api_key.created", "create-bad", []byte(`[]`))
	if _, _, err := store.CreateOrgAPIKeyWithActivity(ctx, orgID.String(), accountID, initialHash, "deploy-ci", []string{"deploy:write"}, &expiry, "", "", nil, badCreate); err == nil {
		t.Fatal("create with invalid activity succeeded")
	}
	if keys, err := store.ListOrgAPIKeys(ctx, orgID.String()); err != nil || len(keys) != 0 {
		t.Fatalf("keys after rejected create/activity = (%#v, %v), want none", keys, err)
	}

	created, createID, err := store.CreateOrgAPIKeyWithActivity(ctx, orgID.String(), accountID, initialHash, "deploy-ci", []string{"deploy:write"}, &expiry, "", "", nil,
		newActivity("api_key.created", "create-1", []byte(`{"scopes":["deploy:write"]}`)))
	if err != nil || createID == 0 {
		t.Fatalf("create key with activity = (%+v, %d, %v)", created, createID, err)
	}
	createdActivity, err := store.ClaimOrgActivityOutbox(ctx, "api-key-activity-test", time.Minute)
	if err != nil || createdActivity.ID != createID || createdActivity.Activity.ResourceID != created.ID || createdActivity.Activity.ResourceLabel != "deploy-ci" {
		t.Fatalf("create activity = (%+v, %v), want created key identity", createdActivity, err)
	}

	const rotatedHash = "rotated-stored-hash-not-for-timeline"
	rotated, old, rotateID, err := store.RotateOrgAPIKeyWithActivity(ctx, orgID.String(), created.ID, []byte(rotatedHash), "deploy-ci-v2", 24*time.Hour, "", "", nil,
		newActivity("api_key.rotated", "rotate-1", []byte(`{"old_key_id":"`+created.ID+`","grace_window_days":1}`)))
	if err != nil || rotateID == 0 || old.Status != string(APIKeyStatusGrace) {
		t.Fatalf("rotate key with activity = (%+v, %+v, %d, %v)", rotated, old, rotateID, err)
	}
	rotatedActivity, err := store.ClaimOrgActivityOutbox(ctx, "api-key-activity-test", time.Minute)
	if err != nil || rotatedActivity.ID != rotateID || rotatedActivity.Activity.ResourceID != rotated.ID || rotatedActivity.Activity.ResourceLabel != "deploy-ci-v2" {
		t.Fatalf("rotation activity = (%+v, %v), want replacement key identity", rotatedActivity, err)
	}

	revoked, revokeID, err := store.RevokeOrgAPIKeyWithActivity(ctx, orgID.String(), rotated.ID,
		newActivity("api_key.revoked", "revoke-1", []byte(`{"reason":"manual"}`)))
	if err != nil || revokeID == 0 || revoked.Status != string(APIKeyStatusRevoked) {
		t.Fatalf("revoke key with activity = (%+v, %d, %v)", revoked, revokeID, err)
	}
	if _, duplicateID, err := store.RevokeOrgAPIKeyWithActivity(ctx, orgID.String(), rotated.ID,
		newActivity("api_key.revoked", "revoke-duplicate", []byte(`{"reason":"manual"}`))); err != nil || duplicateID != 0 {
		t.Fatalf("repeat revoke = (%d, %v), want idempotent revoke without a duplicate timeline event", duplicateID, err)
	}

	for _, id := range []int64{createID, rotateID, revokeID} {
		if delivered, err := store.DeliverOrgActivityOutbox(ctx, id); err != nil || !delivered {
			t.Fatalf("deliver event %d = (%v, %v), want true", id, delivered, err)
		}
	}
	rows, err := store.ListOrgActivity(ctx, OrgActivityFilter{OrgID: orgID, Limit: 10})
	if err != nil || len(rows) != 3 {
		t.Fatalf("org API key activity = (%#v, %v), want create/rotate/revoke", rows, err)
	}
	kinds := map[string]bool{}
	for _, row := range rows {
		kinds[row.Kind] = true
		if strings.Contains(string(row.Data), plaintext) || strings.Contains(string(row.Data), string(initialHash)) || strings.Contains(string(row.Data), rotatedHash) {
			t.Errorf("API key activity leaked credential material: %+v", row)
		}
	}
	if !kinds["api_key.created"] || !kinds["api_key.rotated"] || !kinds["api_key.revoked"] {
		t.Fatalf("API key activity kinds = %v, want create/rotate/revoke", kinds)
	}
	if _, _, err := store.CreateOrgAPIKeyWithActivity(ctx, orgID.String(), accountID, []byte(plaintext), "unsafe-test-only", nil, nil, "", "", nil,
		newActivity("api_key.created", "secret-is-never-event-data", []byte(`{"scopes":[]}`))); err != nil {
		t.Fatalf("create key for redaction assertion: %v", err)
	}
	item, err := store.ClaimOrgActivityOutbox(ctx, "api-key-activity-test", time.Minute)
	if err != nil {
		t.Fatalf("claim last event: %v", err)
	}
	if strings.Contains(string(item.Activity.Data), plaintext) || strings.Contains(item.Activity.ResourceLabel, plaintext) {
		t.Fatalf("API key plaintext leaked into activity: %+v", item.Activity)
	}
}
