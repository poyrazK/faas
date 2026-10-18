package state

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ApplicationStandardExceptionStore = (*MemStore)(nil)

func (m *MemStore) ApproveApplicationStandardException(ctx context.Context, orgID, actorID, appID string, r ApplicationStandardExceptionRequest) (ApplicationStandardException, error) {
	x, err := prepareStandardException(orgID, actorID, appID, r)
	if err != nil {
		return x, err
	}
	if err := ctx.Err(); err != nil {
		return x, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, before, key, err := m.standardExceptionInputsLocked(ctx, orgID, actorID, appID)
	if err != nil {
		return ApplicationStandardException{}, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	x.CreatedAt = now
	if err := validateStandardException(s, before, x, r.ExpectedRevision, now); err != nil {
		return ApplicationStandardException{}, err
	}
	if err := ctx.Err(); err != nil {
		return ApplicationStandardException{}, err
	}
	if m.applicationStandardExceptions == nil {
		m.applicationStandardExceptions = map[string]ApplicationStandardException{}
	}
	m.applicationStandardExceptions[x.ID] = cloneStandardException(x)
	m.standardExceptionPendingLocked(key, before, now)
	m.appendAuditLogLocked(standardExceptionAudit(x, before, actorID, "approved", now))
	return cloneStandardException(x), nil
}

func (m *MemStore) standardExceptionInputsLocked(ctx context.Context, orgID, actorID, appID string) (standardReviewSnapshot, ApplicationStandardEnrollment, string, error) {
	var s standardReviewSnapshot
	a, err := m.standardMemberAuthorityLocked(orgID, actorID, OrgRoleOwner, OrgRoleAdmin)
	if err != nil {
		return s, ApplicationStandardEnrollment{}, "", err
	}
	if err := authorizeStandardApproval(a); err != nil {
		return s, ApplicationStandardEnrollment{}, "", err
	}
	key, before, err := m.standardLocalIntentEnrollmentLocked(orgID, appID)
	if err != nil {
		return s, before, key, err
	}
	if m.standardLocalIntentOperationLocked(appID) {
		return s, before, key, ErrApplicationStandardOperationInProgress
	}
	r := ApplicationStandardReviewRequest{Scope: "application", ScopeID: canonicalStandardUUID(appID), StandardID: "00000000-0000-0000-0000-000000000000"}
	s, err = m.standardReviewSnapshotLocked(ctx, orgID, actorID, r)
	return s, before, key, err
}

func (m *MemStore) RevokeApplicationStandardException(ctx context.Context, orgID, actorID, appID, exceptionID string, expected int64) (ApplicationStandardException, error) {
	if !standardApprovalIdentityValid(orgID, actorID, appID) || !validStandardResourceRead(orgID, exceptionID) || expected <= 0 || expected >= api.ApplicationStandardMaxVersion {
		return ApplicationStandardException{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return ApplicationStandardException{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, before, key, err := m.standardExceptionInputsLocked(ctx, orgID, actorID, appID)
	if err != nil {
		return ApplicationStandardException{}, err
	}
	if before.DesiredRevision != expected {
		return ApplicationStandardException{}, ErrApplicationStandardLocalIntentStale
	}
	x, ok := m.applicationStandardExceptions[canonicalStandardUUID(exceptionID)]
	if !ok || !sameStandardUUID(x.OrgID, orgID) || !sameStandardUUID(x.AppID, appID) {
		return ApplicationStandardException{}, ErrNotFound
	}
	if x.RevokedAt != nil {
		return ApplicationStandardException{}, ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return ApplicationStandardException{}, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	x.RevokedAt = &now
	x.RevokedBy = canonicalStandardUUID(actorID)
	m.applicationStandardExceptions[x.ID] = cloneStandardException(x)
	m.standardExceptionPendingLocked(key, before, now)
	m.appendAuditLogLocked(standardExceptionAudit(x, before, actorID, "revoked", now))
	return cloneStandardException(x), nil
}

func (m *MemStore) standardExceptionPendingLocked(key string, before ApplicationStandardEnrollment, now time.Time) {
	after := cloneApplicationStandardEnrollment(before)
	after.DesiredRevision++
	after.State, after.ErrorCode, after.UpdatedAt = "pending", "", now
	m.applicationStandardEnrollments[key] = after
	if m.applicationStandardEnrollmentClaims == nil {
		m.applicationStandardEnrollmentClaims = map[string]ApplicationStandardEnrollmentClaim{}
	}
	id := canonicalStandardUUID(before.AppID)
	c := m.applicationStandardEnrollmentClaims[id]
	c.Generation++
	c.Owner, c.Until = "", time.Time{}
	m.applicationStandardEnrollmentClaims[id] = c
}

func (m *MemStore) standardActiveExceptionsLocked(orgID, appID string, now time.Time) []ApplicationStandardException {
	xs := []ApplicationStandardException{}
	for _, x := range m.applicationStandardExceptions {
		if sameStandardUUID(x.OrgID, orgID) && sameStandardUUID(x.AppID, appID) && x.RevokedAt == nil && x.ExpiresAt.After(now) {
			xs = append(xs, cloneStandardException(x))
		}
	}
	slices.SortFunc(xs, func(a, b ApplicationStandardException) int { return strings.Compare(a.ID, b.ID) })
	return xs
}

func (m *MemStore) ListApplicationStandardExceptions(ctx context.Context, orgID, appID, after string) ([]ApplicationStandardException, error) {
	if !validStandardResourceRead(orgID, appID) || after != "" && !validStandardResourceRead(orgID, after) {
		return nil, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, _, err := m.standardLocalIntentEnrollmentLocked(orgID, appID); err != nil {
		return nil, err
	}
	xs := []ApplicationStandardException{}
	for _, x := range m.applicationStandardExceptions {
		if sameStandardUUID(x.OrgID, orgID) && sameStandardUUID(x.AppID, appID) && (after == "" || x.ID > canonicalStandardUUID(after)) {
			xs = append(xs, cloneStandardException(x))
		}
	}
	slices.SortFunc(xs, func(a, b ApplicationStandardException) int { return strings.Compare(a.ID, b.ID) })
	if len(xs) > api.ApplicationStandardMaxListPage {
		xs = xs[:api.ApplicationStandardMaxListPage]
	}
	return xs, nil
}

func (m *MemStore) eraseStandardAppExceptionsLocked(appID string) {
	for id, x := range m.applicationStandardExceptions {
		if sameStandardUUID(x.AppID, appID) {
			delete(m.applicationStandardExceptions, id)
		}
	}
}
