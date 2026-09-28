package main

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func newOrgAccessActivity(r *http.Request, acct state.Account, orgID, kind, resourceType string, data map[string]any) state.OrgActivity {
	parsedOrgID, _ := uuid.Parse(orgID)
	if data == nil {
		data = map[string]any{}
	}
	actorType, actorLabel, actorID := activityActor(r, acct)
	label := "member"
	if resourceType == "invitation" {
		label = "invitation"
	}
	return state.OrgActivity{
		OrgID: parsedOrgID, Kind: kind, ActorType: actorType, ActorAccountID: actorID, ActorLabel: actorLabel,
		ResourceType: resourceType, ResourceLabel: label,
		SourceType: kind, SourceID: kind + ":" + uuid.NewString(), Data: activityData(data),
	}
}

func orgInvitationActivityFor(entry state.OrgActivity, invitation state.OrgInvitation) state.OrgActivity {
	orgID, _ := uuid.Parse(invitation.OrgID)
	entry.OrgID = orgID
	entry.ResourceType = "invitation"
	entry.ResourceID = invitation.ID
	if strings.TrimSpace(invitation.Email) != "" {
		entry.ResourceLabel = invitation.Email
	}
	return entry
}

func orgMemberActivityFor(entry state.OrgActivity, membership state.OrgMembership, email string) state.OrgActivity {
	orgID, _ := uuid.Parse(membership.OrgID)
	entry.OrgID = orgID
	entry.ResourceType = "member"
	entry.ResourceID = membership.AccountID
	if strings.TrimSpace(email) != "" {
		entry.ResourceLabel = email
	}
	return entry
}

func (s *server) createOrgInvitationWithActivity(ctx context.Context, invitation state.OrgInvitation, activity state.OrgActivity) (state.OrgInvitation, error) {
	if mutations, ok := s.store.(state.OrgActivityAccessMutationStore); ok {
		created, outboxID, err := mutations.CreateOrgInvitationWithActivity(ctx, invitation, activity)
		if err == nil && outboxID > 0 {
			s.deliverOrgActivityOutbox(ctx, outboxID)
		}
		return created, err
	}
	created, err := s.store.CreateOrgInvitation(ctx, invitation)
	if err == nil {
		s.recordOrgActivity(ctx, orgInvitationActivityFor(activity, created))
	}
	return created, err
}

func (s *server) consumeOrgInvitationWithActivity(ctx context.Context, hash []byte, accepting state.Account, acceptedActivity, memberActivity state.OrgActivity) (state.OrgMembership, state.OrgInvitation, error) {
	if mutations, ok := s.store.(state.OrgActivityAccessMutationStore); ok {
		membership, invitation, acceptedID, memberID, err := mutations.ConsumeOrgInvitationWithActivity(ctx, hash, accepting, acceptedActivity, memberActivity)
		if err == nil {
			if acceptedID > 0 {
				s.deliverOrgActivityOutbox(ctx, acceptedID)
			}
			if memberID > 0 {
				s.deliverOrgActivityOutbox(ctx, memberID)
			}
		}
		return membership, invitation, err
	}
	membership, invitation, err := s.store.ConsumeOrgInvitation(ctx, hash, accepting)
	if err != nil {
		return state.OrgMembership{}, state.OrgInvitation{}, err
	}
	acceptedActivity.Data = activityData(map[string]any{"role": string(invitation.Role), "invitation_id": invitation.ID})
	memberActivity.Data = activityData(map[string]any{"role": string(membership.Role), "invitation_id": invitation.ID})
	s.recordOrgActivity(ctx, orgInvitationActivityFor(acceptedActivity, invitation))
	s.recordOrgActivity(ctx, orgMemberActivityFor(memberActivity, membership, accepting.Email))
	return membership, invitation, nil
}

func (s *server) revokeOrgInvitationWithActivity(ctx context.Context, orgID, invitationID, actorID string, activity state.OrgActivity) error {
	if mutations, ok := s.store.(state.OrgActivityAccessMutationStore); ok {
		_, outboxID, err := mutations.RevokeOrgInvitationWithActivity(ctx, orgID, invitationID, actorID, activity)
		if err == nil && outboxID > 0 {
			s.deliverOrgActivityOutbox(ctx, outboxID)
		}
		return err
	}
	var prior state.OrgInvitation
	if rows, err := s.store.ListOrgInvitationsForOrg(ctx, orgID); err == nil {
		for _, candidate := range rows {
			if candidate.ID == invitationID {
				prior = candidate
				break
			}
		}
	}
	if err := s.store.RevokeOrgInvitation(ctx, orgID, invitationID, actorID); err != nil {
		return err
	}
	if prior.ID != "" {
		activity.Data = activityData(map[string]any{"role": string(prior.Role)})
		s.recordOrgActivity(ctx, orgInvitationActivityFor(activity, prior))
	}
	return nil
}

func (s *server) updateOrgMemberRoleWithActivity(ctx context.Context, orgID, accountID string, role state.OrgRole, activity state.OrgActivity) error {
	if mutations, ok := s.store.(state.OrgActivityAccessMutationStore); ok {
		_, outboxID, err := mutations.UpdateOrgMemberRoleWithActivity(ctx, orgID, accountID, role, activity)
		if err == nil && outboxID > 0 {
			s.deliverOrgActivityOutbox(ctx, outboxID)
		}
		return err
	}
	prior, priorErr := s.store.OrgMemberByAccount(ctx, orgID, accountID)
	if err := s.store.UpdateOrgMemberRole(ctx, orgID, accountID, role); err != nil {
		return err
	}
	if priorErr == nil && prior.Role != role {
		activity.Data = activityData(map[string]any{"old_role": string(prior.Role), "new_role": string(role)})
		updated, err := s.store.OrgMemberByAccount(ctx, orgID, accountID)
		if err == nil {
			email := ""
			if account, accountErr := s.store.AccountByID(ctx, accountID); accountErr == nil {
				email = account.Email
			}
			s.recordOrgActivity(ctx, orgMemberActivityFor(activity, updated, email))
		}
	}
	return nil
}

func (s *server) removeOrgMemberWithActivity(ctx context.Context, orgID, accountID string, activity state.OrgActivity) error {
	if mutations, ok := s.store.(state.OrgActivityAccessMutationStore); ok {
		_, outboxID, err := mutations.RemoveOrgMemberWithActivity(ctx, orgID, accountID, activity)
		if err == nil && outboxID > 0 {
			s.deliverOrgActivityOutbox(ctx, outboxID)
		}
		return err
	}
	prior, priorErr := s.store.OrgMemberByAccount(ctx, orgID, accountID)
	if err := s.store.RemoveOrgMember(ctx, orgID, accountID); err != nil {
		return err
	}
	if priorErr == nil && prior.RemovedAt == nil {
		activity.Data = activityData(map[string]any{"role": string(prior.Role)})
		email := ""
		if account, accountErr := s.store.AccountByID(ctx, accountID); accountErr == nil {
			email = account.Email
		}
		s.recordOrgActivity(ctx, orgMemberActivityFor(activity, prior, email))
	}
	return nil
}

func (s *server) transferOrgOwnershipWithActivity(ctx context.Context, orgID, fromAccountID, toAccountID string, activity state.OrgActivity) error {
	if mutations, ok := s.store.(state.OrgActivityOwnershipTransferMutationStore); ok {
		outboxID, err := mutations.TransferOrgOwnershipWithActivity(ctx, orgID, fromAccountID, toAccountID, activity)
		if err == nil && outboxID > 0 {
			s.deliverOrgActivityOutbox(ctx, outboxID)
		}
		return err
	}
	if err := s.store.TransferOrgOwnership(ctx, orgID, fromAccountID, toAccountID); err != nil {
		return err
	}
	membership, err := s.store.OrgMemberByAccount(ctx, orgID, toAccountID)
	if err == nil {
		email := ""
		if account, accountErr := s.store.AccountByID(ctx, toAccountID); accountErr == nil {
			email = account.Email
		}
		activity.Data = activityData(map[string]any{
			"previous_owner_account_id": fromAccountID,
			"new_owner_account_id":      toAccountID,
		})
		s.recordOrgActivity(ctx, orgMemberActivityFor(activity, membership, email))
	}
	return nil
}
