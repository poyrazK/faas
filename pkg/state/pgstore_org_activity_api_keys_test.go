package state_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreOrgAPIKeyMutationsAreAtomicWithActivity(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	accountOrg, err := s.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{
		Email: "org-activity-api-key-" + uuid.NewString() + "@example.com", Plan: api.PlanPro,
	})
	if err != nil {
		t.Fatalf("CreateAccountWithPersonalOrg: %v", err)
	}
	accountID, org := accountOrg.Account.ID, accountOrg.PersonalOrg
	orgID, actorID := uuid.MustParse(org.ID), uuid.MustParse(accountID)
	newActivity := func(kind, sourceID string, data []byte) state.OrgActivity {
		return state.OrgActivity{
			OrgID: orgID, Kind: kind, ActorType: state.OrgActivityActorUser,
			ActorAccountID: &actorID, ActorLabel: "owner@example.com",
			ResourceType: "api_key", ResourceLabel: "API key", SourceType: kind,
			SourceID: sourceID, Data: data,
		}
	}
	expiresAt := time.Now().UTC().Add(24 * time.Hour)
	badActivity := newActivity("api_key.created", "bad-create", []byte(`[]`))
	if _, _, err := s.CreateOrgAPIKeyWithActivity(ctx, org.ID, accountID, []byte("invalid-activity-hash"), "ci", []string{"deploy:write"}, &expiresAt, "", "", nil, badActivity); err == nil {
		t.Fatal("create with invalid activity succeeded")
	}
	if keys, err := s.ListAPIKeys(ctx, accountID); err != nil || len(keys) != 0 {
		t.Fatalf("keys after rejected create/activity = (%#v, %v), want none", keys, err)
	}

	created, createID, err := s.CreateOrgAPIKeyWithActivity(ctx, org.ID, accountID, []byte("initial-hash"), "ci", []string{"deploy:write"}, &expiresAt, "", "", nil,
		newActivity("api_key.created", "create-1", []byte(`{"scopes":["deploy:write"]}`)))
	if err != nil || createID == 0 {
		t.Fatalf("create key with activity = (%+v, %d, %v)", created, createID, err)
	}
	rotated, old, rotateID, err := s.RotateOrgAPIKeyWithActivity(ctx, org.ID, created.ID, []byte("rotated-hash"), "ci-next", 24*time.Hour, "", "", nil,
		newActivity("api_key.rotated", "rotate-1", []byte(`{"old_key_id":"`+created.ID+`","grace_window_days":1}`)))
	if err != nil || rotateID == 0 || old.Status != string(state.APIKeyStatusGrace) {
		t.Fatalf("rotate key with activity = (%+v, %+v, %d, %v)", rotated, old, rotateID, err)
	}
	revoked, revokeID, err := s.RevokeOrgAPIKeyWithActivity(ctx, org.ID, rotated.ID,
		newActivity("api_key.revoked", "revoke-1", []byte(`{"reason":"manual"}`)))
	if err != nil || revokeID == 0 || revoked.Status != string(state.APIKeyStatusRevoked) {
		t.Fatalf("revoke key with activity = (%+v, %d, %v)", revoked, revokeID, err)
	}
	if _, duplicateID, err := s.RevokeOrgAPIKeyWithActivity(ctx, org.ID, rotated.ID,
		newActivity("api_key.revoked", "revoke-duplicate", []byte(`{"reason":"manual"}`))); err != nil || duplicateID != 0 {
		t.Fatalf("repeat revoke = (%d, %v), want idempotent revoke without a duplicate event", duplicateID, err)
	}

	for _, id := range []int64{createID, rotateID, revokeID} {
		if delivered, err := s.DeliverOrgActivityOutbox(ctx, id); err != nil || !delivered {
			t.Fatalf("deliver event %d = (%v, %v), want true", id, delivered, err)
		}
	}
	rows, err := s.ListOrgActivity(ctx, state.OrgActivityFilter{OrgID: orgID, Limit: 10})
	if err != nil || len(rows) != 3 {
		t.Fatalf("org API key activity = (%#v, %v), want create/rotate/revoke", rows, err)
	}
	for _, row := range rows {
		if strings.Contains(string(row.Data), "initial-hash") || strings.Contains(string(row.Data), "rotated-hash") {
			t.Errorf("API-key activity leaked stored hash: %+v", row)
		}
	}
}
