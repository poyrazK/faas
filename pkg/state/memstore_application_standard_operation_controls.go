package state

import (
	"context"
	"time"
)

var _ ApplicationStandardOperationControlStore = (*MemStore)(nil)

func (m *MemStore) ControlApplicationStandardOperation(ctx context.Context, orgID, actorID, operationID string, expected time.Time, action ApplicationStandardOperationAction) (ApplicationStandardOperation, error) {
	if !validStandardOperationControl(orgID, actorID, operationID, expected, action) {
		return ApplicationStandardOperation{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return ApplicationStandardOperation{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	authority, err := m.standardOperationAuthorityLocked(orgID, actorID)
	if err != nil {
		return ApplicationStandardOperation{}, err
	}
	if err := authorizeStandardApproval(authority); err != nil {
		return ApplicationStandardOperation{}, err
	}
	before, exists := m.applicationStandardOperations[canonicalStandardUUID(operationID)]
	if !exists || !sameStandardUUID(before.OrgID, orgID) {
		return ApplicationStandardOperation{}, ErrNotFound
	}
	after, err := prepareStandardOperationControl(before, expected, action, time.Now().UTC())
	if err != nil || after.UpdatedAt.Equal(before.UpdatedAt) {
		return cloneStandardOperation(after), err
	}
	if err := ctx.Err(); err != nil {
		return ApplicationStandardOperation{}, err
	}
	if m.applicationStandardWorkerClaims == nil {
		m.applicationStandardWorkerClaims = map[string]ApplicationStandardWorkerClaim{}
	}
	held := m.applicationStandardWorkerClaims[after.ID]
	held.Generation++
	held.Owner, held.Until = "", time.Time{}
	m.applicationStandardWorkerClaims[after.ID] = held
	m.applicationStandardOperations[after.ID] = cloneStandardOperation(after)
	m.appendAuditLogLocked(standardOperationControlAudit(before, after, actorID, action))
	return cloneStandardOperation(after), nil
}

func (m *MemStore) standardOperationAuthorityLocked(orgID, actorID string) (standardReviewSnapshot, error) {
	s := standardReviewSnapshot{}
	found := false
	for _, org := range m.orgs {
		if sameStandardUUID(org.ID, orgID) {
			s.OrgID, s.OrgStatus, s.DeletedPending, found = canonicalStandardUUID(org.ID), string(org.Status), org.DeletedPending, true
			break
		}
	}
	if !found {
		return s, ErrNotFound
	}
	for _, membership := range m.memberships {
		if !sameStandardUUID(membership.OrgID, orgID) || !sameStandardUUID(membership.AccountID, actorID) || membership.RemovedAt != nil || (membership.Role != OrgRoleOwner && membership.Role != OrgRoleAdmin) {
			continue
		}
		for _, account := range m.accounts {
			if sameStandardUUID(account.ID, actorID) && account.Status == AccountActive {
				s.ActorAuthorized = true
			}
		}
	}
	return s, nil
}
