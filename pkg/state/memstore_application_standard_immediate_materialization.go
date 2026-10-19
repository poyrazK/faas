package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ApplicationStandardImmediateMaterializationStore = (*MemStore)(nil)

func (m *MemStore) ClaimApplicationStandardEnrollmentForApp(ctx context.Context, r ApplicationStandardEnrollmentClaimRequest) (ApplicationStandardEnrollmentClaim, error) {
	if !validStandardImmediateClaim(r) {
		return ApplicationStandardEnrollmentClaim{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ApplicationStandardEnrollmentClaim{}, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, e := range m.applicationStandardEnrollments {
		if !sameStandardUUID(e.AppID, r.AppID) || !sameStandardUUID(e.OrgID, r.OrgID) || e.State != "pending" || e.DesiredRevision != r.DesiredRevision {
			continue
		}
		id := canonicalStandardUUID(e.AppID)
		held := m.applicationStandardEnrollmentClaims[id]
		if held.Owner != "" && held.Until.After(now) || m.standardQueuedTargetLocked(id) || !m.standardImmediateAppLiveLocked(id) {
			return ApplicationStandardEnrollmentClaim{}, ErrNotFound
		}
		c := ApplicationStandardEnrollmentClaim{AppID: id, OrgID: canonicalStandardUUID(e.OrgID), Owner: r.Owner, Generation: held.Generation + 1, DesiredRevision: e.DesiredRevision, Until: now.Add(api.ApplicationStandardWorkerLease)}
		if m.applicationStandardEnrollmentClaims == nil {
			m.applicationStandardEnrollmentClaims = map[string]ApplicationStandardEnrollmentClaim{}
		}
		m.applicationStandardEnrollmentClaims[id] = c
		return c, nil
	}
	return ApplicationStandardEnrollmentClaim{}, ErrNotFound
}

func (m *MemStore) standardImmediateAppLiveLocked(id string) bool {
	for _, a := range m.apps {
		if sameStandardUUID(a.ID, id) {
			return a.Status != AppDeleted
		}
	}
	return false
}

func (m *MemStore) ReleaseApplicationStandardEnrollmentWorker(ctx context.Context, c ApplicationStandardEnrollmentClaim) error {
	if !standardEnrollmentClaimValid(c) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	id := canonicalStandardUUID(c.AppID)
	held := m.applicationStandardEnrollmentClaims[id]
	if !sameStandardUUID(held.OrgID, c.OrgID) || held.Owner != c.Owner || held.Generation != c.Generation {
		return ErrApplicationStandardLeaseLost
	}
	held.Owner, held.Until = "", time.Time{}
	m.applicationStandardEnrollmentClaims[id] = held
	return nil
}
