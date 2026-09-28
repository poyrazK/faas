package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func newOrgAPIKeyActivity(r *http.Request, acct state.Account, orgID, kind string, data map[string]any) state.OrgActivity {
	id, _ := uuid.Parse(orgID)
	actorType, actorLabel, actorAccountID := activityActor(r, acct)
	return state.OrgActivity{
		OrgID: id, Kind: kind, ActorType: actorType, ActorAccountID: actorAccountID, ActorLabel: actorLabel,
		ResourceType: "api_key", ResourceLabel: "API key",
		SourceType: kind, SourceID: kind + ":" + uuid.NewString(), Data: activityData(data),
	}
}

func orgAPIKeyActivityFor(activity state.OrgActivity, key state.APIKey) state.OrgActivity {
	activity.ResourceType = "api_key"
	activity.ResourceID = key.ID
	if label := strings.TrimSpace(key.Label); label != "" {
		activity.ResourceLabel = label
	} else {
		activity.ResourceLabel = "API key"
	}
	return activity
}

func (s *server) recordOrgActivity(ctx context.Context, entry state.OrgActivity) {
	activityStore, hasActivityStore := s.store.(state.OrgActivityStore)
	outbox, hasOutbox := s.store.(state.OrgActivityOutboxStore)
	if hasOutbox {
		id, err := outbox.EnqueueOrgActivityOutbox(ctx, entry)
		if err != nil {
			if s.log != nil {
				s.log.Warn("activity: enqueue failed", "org", entry.OrgID.String(), "kind", entry.Kind, "err", err)
			}
			return
		}
		s.deliverOrgActivityOutbox(ctx, id)
		return
	}
	if hasActivityStore {
		if _, err := activityStore.AppendOrgActivity(ctx, entry); err != nil && s.log != nil {
			s.log.Warn("activity: append failed", "org", entry.OrgID.String(), "kind", entry.Kind, "err", err)
		}
	}
}

func (s *server) createOrgAPIKeyWithActivity(ctx context.Context, acct state.Account, orgID string, hash []byte, label string, scopes []string, expiresAt *time.Time, createdIP, createdUA string, parent *string, activity state.OrgActivity) (state.APIKey, error) {
	if mutations, ok := s.store.(state.OrgActivityAPIKeyMutationStore); ok {
		key, outboxID, err := mutations.CreateOrgAPIKeyWithActivity(ctx, orgID, acct.ID, hash, label, scopes, expiresAt, createdIP, createdUA, parent, activity)
		if err == nil && outboxID > 0 {
			s.deliverOrgActivityOutbox(ctx, outboxID)
		}
		return key, err
	}
	key, err := s.store.CreateOrgAPIKeyWithProvenance(ctx, orgID, acct.ID, hash, label, scopes, expiresAt, createdIP, createdUA, parent)
	if err == nil {
		s.recordOrgActivity(ctx, orgAPIKeyActivityFor(activity, key))
	}
	return key, err
}

func (s *server) revokeOrgAPIKeyWithActivity(ctx context.Context, orgID, keyID string, activity state.OrgActivity) (state.APIKey, error) {
	// MemStore retains support for pre-org account keys used by legacy
	// handler tests. Such keys cannot be represented in the workspace
	// activity timeline, so preserve the mutation without attempting to
	// normalize an activity row with a missing org ID.
	if orgID == "" {
		return s.store.RevokeOrgAPIKey(ctx, orgID, keyID)
	}
	if mutations, ok := s.store.(state.OrgActivityAPIKeyMutationStore); ok {
		key, outboxID, err := mutations.RevokeOrgAPIKeyWithActivity(ctx, orgID, keyID, activity)
		if err == nil && outboxID > 0 {
			s.deliverOrgActivityOutbox(ctx, outboxID)
		}
		return key, err
	}
	prior, priorErr := s.store.GetOrgAPIKey(ctx, orgID, keyID)
	key, err := s.store.RevokeOrgAPIKey(ctx, orgID, keyID)
	if err == nil && (priorErr != nil || prior.Status != string(state.APIKeyStatusRevoked)) {
		s.recordOrgActivity(ctx, orgAPIKeyActivityFor(activity, key))
	}
	return key, err
}

func (s *server) rotateOrgAPIKeyWithActivity(ctx context.Context, orgID, oldKeyID string, hash []byte, label string, graceWindow time.Duration, createdIP, createdUA string, parent *string, activity state.OrgActivity) (state.APIKey, state.APIKey, error) {
	// MemStore retains support for pre-org account keys used by legacy
	// handler tests. Such keys cannot be represented in the workspace
	// activity timeline, but still need the provenance stamps and atomic
	// rotation behavior exercised by the account-key endpoint.
	if orgID == "" {
		return s.store.RotateOrgAPIKeyWithProvenance(ctx, orgID, oldKeyID, hash, label, graceWindow, createdIP, createdUA, parent)
	}
	if mutations, ok := s.store.(state.OrgActivityAPIKeyMutationStore); ok {
		newKey, oldKey, outboxID, err := mutations.RotateOrgAPIKeyWithActivity(ctx, orgID, oldKeyID, hash, label, graceWindow, createdIP, createdUA, parent, activity)
		if err == nil && outboxID > 0 {
			s.deliverOrgActivityOutbox(ctx, outboxID)
		}
		return newKey, oldKey, err
	}
	newKey, oldKey, err := s.store.RotateOrgAPIKeyWithProvenance(ctx, orgID, oldKeyID, hash, label, graceWindow, createdIP, createdUA, parent)
	if err == nil {
		s.recordOrgActivity(ctx, orgAPIKeyActivityFor(activity, newKey))
	}
	return newKey, oldKey, err
}
