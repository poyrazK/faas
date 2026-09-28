package state

import (
	"context"
	"encoding/hex"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ OrgActivityAccessMutationStore = (*MemStore)(nil)

func (m *MemStore) CreateOrgInvitationWithActivity(_ context.Context, invitation OrgInvitation, activity OrgActivity) (OrgInvitation, int64, error) {
	if invitation.ID == "" {
		invitation.ID = newID()
	}
	if invitation.CreatedAt.IsZero() {
		invitation.CreatedAt = time.Now().UTC()
	}
	activity, err := withOrgActivityData(activity, map[string]any{
		"role": string(invitation.Role), "expires_at": invitation.ExpiresAt.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return OrgInvitation{}, 0, err
	}
	activity, err = bindOrgActivityToInvitation(activity, invitation)
	if err != nil {
		return OrgInvitation{}, 0, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	hashKey := hex.EncodeToString(invitation.TokenHash)
	if _, exists := m.invitations[hashKey]; exists {
		return OrgInvitation{}, 0, ErrConflict
	}
	m.invitations[hashKey] = invitation
	return invitation, m.enqueueOrgActivityOutboxLocked(activity), nil
}

func (m *MemStore) ConsumeOrgInvitationWithActivity(_ context.Context, hash []byte, accepting Account, acceptedActivity, memberActivity OrgActivity) (OrgMembership, OrgInvitation, int64, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	hashKey := hex.EncodeToString(hash)
	invitation, ok := m.invitations[hashKey]
	if !ok || invitation.ConsumedAt != nil || invitation.RevokedAt != nil {
		return OrgMembership{}, OrgInvitation{}, 0, 0, ErrOrgInvitationInvalid
	}
	if !invitation.ExpiresAt.IsZero() && invitation.ExpiresAt.Before(time.Now().UTC()) {
		return OrgMembership{}, OrgInvitation{}, 0, 0, ErrOrgInvitationExpired
	}
	if !strings.EqualFold(invitation.Email, accepting.Email) {
		return OrgMembership{}, OrgInvitation{}, 0, 0, ErrOrgInvitationInvalid
	}
	org, ok := m.orgs[invitation.OrgID]
	if !ok {
		return OrgMembership{}, OrgInvitation{}, 0, 0, ErrNotFound
	}
	limits, _ := api.LimitsFor(org.Plan)
	if limit := limits.OrgMembersMax; limit > 0 {
		active := 0
		for _, existing := range m.memberships {
			if existing.OrgID == invitation.OrgID && existing.RemovedAt == nil {
				active++
			}
		}
		if active >= limit {
			return OrgMembership{}, OrgInvitation{}, 0, 0, ErrOrgMemberCapExceeded
		}
	}
	key := orgAccountKey{OrgID: invitation.OrgID, AccountID: accepting.ID}
	if _, exists := m.memberships[key]; exists {
		return OrgMembership{}, OrgInvitation{}, 0, 0, ErrOrgAlreadyMember
	}

	now := time.Now().UTC()
	acceptingID := accepting.ID
	membership := OrgMembership{
		OrgID: invitation.OrgID, AccountID: accepting.ID, Role: invitation.Role,
		InvitedByAccountID: invitation.InvitedByAccountID, JoinedAt: now,
	}
	invitation.ConsumedAt = &now
	invitation.AcceptingAccountID = &acceptingID

	acceptedActivity, err := withOrgActivityData(acceptedActivity, map[string]any{
		"role": string(invitation.Role), "invitation_id": invitation.ID,
	})
	if err != nil {
		return OrgMembership{}, OrgInvitation{}, 0, 0, err
	}
	acceptedActivity, err = bindOrgActivityToInvitation(acceptedActivity, invitation)
	if err != nil {
		return OrgMembership{}, OrgInvitation{}, 0, 0, err
	}
	memberActivity, err = withOrgActivityData(memberActivity, map[string]any{
		"role": string(membership.Role), "invitation_id": invitation.ID,
	})
	if err != nil {
		return OrgMembership{}, OrgInvitation{}, 0, 0, err
	}
	memberActivity, err = bindOrgActivityToMembership(memberActivity, membership, accepting.Email)
	if err != nil {
		return OrgMembership{}, OrgInvitation{}, 0, 0, err
	}

	m.memberships[key] = membership
	m.invitations[hashKey] = invitation
	acceptedOutboxID := m.enqueueOrgActivityOutboxLocked(acceptedActivity)
	memberOutboxID := m.enqueueOrgActivityOutboxLocked(memberActivity)
	return membership, invitation, acceptedOutboxID, memberOutboxID, nil
}

func (m *MemStore) RevokeOrgInvitationWithActivity(_ context.Context, orgID, invitationID, _ string, activity OrgActivity) (OrgInvitation, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var hashKey string
	var invitation OrgInvitation
	for key, candidate := range m.invitations {
		if candidate.ID == invitationID && candidate.OrgID == orgID {
			hashKey, invitation = key, candidate
			break
		}
	}
	if hashKey == "" || invitation.ConsumedAt != nil || invitation.RevokedAt != nil {
		return OrgInvitation{}, 0, ErrOrgInvitationInvalid
	}
	now := time.Now().UTC()
	invitation.RevokedAt = &now
	activity, err := withOrgActivityData(activity, map[string]any{"role": string(invitation.Role)})
	if err != nil {
		return OrgInvitation{}, 0, err
	}
	activity, err = bindOrgActivityToInvitation(activity, invitation)
	if err != nil {
		return OrgInvitation{}, 0, err
	}
	m.invitations[hashKey] = invitation
	return invitation, m.enqueueOrgActivityOutboxLocked(activity), nil
}

func (m *MemStore) UpdateOrgMemberRoleWithActivity(_ context.Context, orgID, accountID string, role OrgRole, activity OrgActivity) (OrgMembership, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := orgAccountKey{OrgID: orgID, AccountID: accountID}
	current, ok := m.memberships[key]
	if !ok {
		return OrgMembership{}, 0, ErrNotFound
	}
	if current.Role == OrgRoleOwner && role != OrgRoleOwner && current.RemovedAt == nil {
		return OrgMembership{}, 0, ErrOrgLastOwner
	}
	if current.Role == role {
		return current, 0, nil
	}
	updated := current
	updated.Role = role
	activity, err := withOrgActivityData(activity, map[string]any{
		"old_role": string(current.Role), "new_role": string(updated.Role),
	})
	if err != nil {
		return OrgMembership{}, 0, err
	}
	label := "member"
	if account, ok := m.accounts[accountID]; ok && strings.TrimSpace(account.Email) != "" {
		label = account.Email
	}
	activity, err = bindOrgActivityToMembership(activity, updated, label)
	if err != nil {
		return OrgMembership{}, 0, err
	}
	m.memberships[key] = updated
	return updated, m.enqueueOrgActivityOutboxLocked(activity), nil
}

func (m *MemStore) RemoveOrgMemberWithActivity(_ context.Context, orgID, accountID string, activity OrgActivity) (OrgMembership, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := orgAccountKey{OrgID: orgID, AccountID: accountID}
	current, ok := m.memberships[key]
	if !ok {
		return OrgMembership{}, 0, ErrNotFound
	}
	if current.Role == OrgRoleOwner && current.RemovedAt == nil {
		return OrgMembership{}, 0, ErrOrgLastOwner
	}
	if current.RemovedAt != nil {
		return current, 0, nil
	}
	now := time.Now().UTC()
	removed := current
	removed.RemovedAt = &now
	activity, err := withOrgActivityData(activity, map[string]any{"role": string(removed.Role)})
	if err != nil {
		return OrgMembership{}, 0, err
	}
	label := "member"
	if account, ok := m.accounts[accountID]; ok && strings.TrimSpace(account.Email) != "" {
		label = account.Email
	}
	activity, err = bindOrgActivityToMembership(activity, removed, label)
	if err != nil {
		return OrgMembership{}, 0, err
	}
	m.memberships[key] = removed
	return removed, m.enqueueOrgActivityOutboxLocked(activity), nil
}
