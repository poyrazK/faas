package state

import (
	"context"
	"time"
)

var _ ApplicationStandardLocalIntentStore = (*MemStore)(nil)

func (m *MemStore) SetApplicationStandardLocalIntent(ctx context.Context, orgID, actorID, appID string, r ApplicationStandardLocalIntentRequest) (ApplicationStandardEnrollment, error) {
	intent, err := prepareStandardLocalIntent(orgID, actorID, appID, r)
	if err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	if err := ctx.Err(); err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	authority, err := m.standardMemberAuthorityLocked(orgID, actorID, OrgRoleOwner, OrgRoleAdmin, OrgRoleDeveloper)
	if err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	if err := authorizeStandardApproval(authority); err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	return m.saveStandardLocalIntentLocked(ctx, orgID, actorID, appID, intent)
}

func (m *MemStore) saveStandardLocalIntentLocked(ctx context.Context, orgID, actorID, appID string, intent standardLocalIntent) (ApplicationStandardEnrollment, error) {
	key, before, err := m.standardLocalIntentEnrollmentLocked(orgID, appID)
	if err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	if m.standardLocalIntentOperationLocked(appID) {
		return ApplicationStandardEnrollment{}, ErrApplicationStandardOperationInProgress
	}
	r := ApplicationStandardReviewRequest{Scope: "application", ScopeID: canonicalStandardUUID(appID), StandardID: "00000000-0000-0000-0000-000000000000"}
	snapshot, err := m.standardReviewSnapshotLocked(ctx, orgID, actorID, r)
	if err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	proposed, changed, err := validateStandardLocalIntent(snapshot, before, intent, now)
	if err != nil || !changed {
		return cloneApplicationStandardEnrollment(before), err
	}
	if err := ctx.Err(); err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	after := standardSavedLocalIntent(before, proposed, now)
	if m.applicationStandardEnrollmentClaims == nil {
		m.applicationStandardEnrollmentClaims = map[string]ApplicationStandardEnrollmentClaim{}
	}
	held := m.applicationStandardEnrollmentClaims[canonicalStandardUUID(appID)]
	held.Generation++
	held.Owner, held.Until = "", time.Time{}
	m.applicationStandardEnrollmentClaims[canonicalStandardUUID(appID)] = held
	m.applicationStandardEnrollments[key] = cloneApplicationStandardEnrollment(after)
	m.appendAuditLogLocked(standardLocalIntentAudit(before, after, proposed, actorID))
	return cloneApplicationStandardEnrollment(after), nil
}

func (m *MemStore) standardLocalIntentEnrollmentLocked(orgID, appID string) (string, ApplicationStandardEnrollment, error) {
	for _, app := range m.apps {
		if sameStandardUUID(app.ID, appID) && sameStandardUUID(app.OrgID, orgID) && app.Status != AppDeleted {
			for key, e := range m.applicationStandardEnrollments {
				if sameStandardUUID(e.AppID, appID) && sameStandardUUID(e.OrgID, orgID) {
					return key, cloneApplicationStandardEnrollment(e), nil
				}
			}
		}
	}
	return "", ApplicationStandardEnrollment{}, ErrNotFound
}

func (m *MemStore) standardLocalIntentOperationLocked(appID string) bool {
	for _, operation := range m.applicationStandardOperations {
		if standardOperationActive(operation.State) {
			for _, target := range operation.Targets {
				if sameStandardUUID(target.AppID, appID) && target.State != "skipped" {
					return true
				}
			}
		}
	}
	return false
}
