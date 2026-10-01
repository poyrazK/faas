package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/appstandards"
)

var _ ApplicationStandardOperationStore = (*MemStore)(nil)

func (m *MemStore) GetApplicationStandardOperation(_ context.Context, orgID, operationID string) (ApplicationStandardOperation, error) {
	if !validStandardResourceRead(orgID, operationID) {
		return ApplicationStandardOperation{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	o, exists := m.applicationStandardOperations[canonicalStandardUUID(operationID)]
	if !exists || !sameStandardUUID(orgID, o.OrgID) {
		return ApplicationStandardOperation{}, ErrNotFound
	}
	return cloneStandardOperation(o), nil
}

func (m *MemStore) ApproveApplicationStandardReview(ctx context.Context, orgID, actorID, planID, expected string) (ApplicationStandardOperation, error) {
	if !standardApprovalIdentityValid(orgID, actorID, planID) {
		return ApplicationStandardOperation{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return ApplicationStandardOperation{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	saved, err := m.standardReviewPlanLocked(orgID, planID)
	if err != nil {
		return ApplicationStandardOperation{}, err
	}
	snapshot, err := m.standardReviewSnapshotLocked(orgID, actorID, saved.Request)
	if err != nil {
		return ApplicationStandardOperation{}, standardReviewFreshnessError(err)
	}
	if err := authorizeStandardApproval(snapshot); err != nil {
		return ApplicationStandardOperation{}, err
	}
	if expected != saved.ApprovalHash {
		return ApplicationStandardOperation{}, ErrApplicationStandardReviewStale
	}
	for _, existing := range m.applicationStandardOperations {
		if existing.OrgID == saved.OrgID && existing.PlanID == saved.ID {
			return cloneStandardOperation(existing), nil
		}
	}
	now := time.Now().UTC()
	fresh, err := buildStandardReview(snapshot, saved.Request, saved.ID, saved.CreatedBy, now)
	if err != nil {
		return ApplicationStandardOperation{}, standardReviewFreshnessError(err)
	}
	if err := validateStandardReviewHash(saved, fresh, expected, now); err != nil {
		return ApplicationStandardOperation{}, err
	}
	for _, existing := range m.applicationStandardOperations {
		if existing.AssignmentID == saved.Request.AssignmentID && standardOperationActive(existing.State) {
			return ApplicationStandardOperation{}, ErrApplicationStandardOperationInProgress
		}
	}
	o, err := newStandardOperation(fresh, actorID, now)
	if err != nil {
		return ApplicationStandardOperation{}, err
	}
	if err := ctx.Err(); err != nil {
		return ApplicationStandardOperation{}, err
	}
	r := saved.Request
	aKey := r.AssignmentID
	var a applicationStandardAssignmentRecord
	if r.ExpectedRevision == 0 {
		a = applicationStandardAssignmentRecord{Assignment: appstandards.Assignment{ID: r.AssignmentID, OrgID: saved.OrgID, Scope: r.Scope, ScopeID: r.ScopeID, StandardID: r.StandardID}, CreatedBy: o.ApprovedBy, CreatedAt: now}
	} else {
		found := false
		for key, record := range m.applicationStandardAssignments {
			if sameStandardUUID(record.ID, r.AssignmentID) {
				aKey, a, found = key, record, true
				break
			}
		}
		if !found {
			return ApplicationStandardOperation{}, ErrApplicationStandardReviewStale
		}
	}
	if m.applicationStandardAssignments == nil {
		m.applicationStandardAssignments = map[string]applicationStandardAssignmentRecord{}
	}
	if m.applicationStandardOperations == nil {
		m.applicationStandardOperations = map[string]ApplicationStandardOperation{}
	}
	a.AdmissionVersion, a.Active, a.Revision, a.UpdatedAt = r.AdmissionVersion, r.Active, r.ExpectedRevision+1, now
	m.applicationStandardAssignments[aKey] = a
	m.applicationStandardOperations[o.ID] = cloneStandardOperation(o)
	m.appendAuditLogLocked(standardApprovalAudit(o))
	return cloneStandardOperation(o), nil
}
