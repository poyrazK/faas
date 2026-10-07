package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ApplicationStandardAutomaticObservationStore = (*MemStore)(nil)

func (m *MemStore) ClaimApplicationStandardObservation(ctx context.Context, owner string) (ApplicationStandardEnrollmentClaim, error) {
	if !standardWorkerOwnerValid(owner) {
		return ApplicationStandardEnrollmentClaim{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ApplicationStandardEnrollmentClaim{}, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	entries := []ApplicationStandardEnrollment{}
	for _, e := range m.applicationStandardEnrollments {
		if m.standardAutomaticObservationEligibleLocked(e, now) {
			entries = append(entries, e)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := m.standardObservationChecks[canonicalStandardUUID(entries[i].AppID)], m.standardObservationChecks[canonicalStandardUUID(entries[j].AppID)]
		if a.at.Equal(b.at) {
			return entries[i].AppID < entries[j].AppID
		}
		return a.at.Before(b.at)
	})
	if len(entries) == 0 {
		return ApplicationStandardEnrollmentClaim{}, ErrNotFound
	}
	e := entries[0]
	id := canonicalStandardUUID(e.AppID)
	c := ApplicationStandardEnrollmentClaim{AppID: id, OrgID: canonicalStandardUUID(e.OrgID), Owner: owner, Generation: m.applicationStandardEnrollmentClaims[id].Generation + 1, DesiredRevision: e.DesiredRevision, Until: now.Add(api.ApplicationStandardWorkerLease)}
	if m.applicationStandardEnrollmentClaims == nil {
		m.applicationStandardEnrollmentClaims = map[string]ApplicationStandardEnrollmentClaim{}
	}
	if m.standardObservationChecks == nil {
		m.standardObservationChecks = map[string]standardAutomaticObservationCheck{}
	}
	m.applicationStandardEnrollmentClaims[id] = c
	m.standardObservationChecks[id] = standardAutomaticObservationCheck{revision: e.DesiredRevision, at: now}
	return c, nil
}

func (m *MemStore) standardAutomaticObservationEligibleLocked(e ApplicationStandardEnrollment, now time.Time) bool {
	if !standardObservationInstalled(e) || m.standardActiveTargetLocked(e.AppID) {
		return false
	}
	id := canonicalStandardUUID(e.AppID)
	held, check := m.applicationStandardEnrollmentClaims[id], m.standardObservationChecks[id]
	if held.Owner != "" && held.Until.After(now) || check.revision == e.DesiredRevision && check.at.Add(api.ApplicationStandardObservationInterval).After(now) {
		return false
	}
	for _, a := range m.apps {
		if sameStandardUUID(a.ID, e.AppID) && a.Status != AppDeleted {
			return true
		}
	}
	return false
}

func (m *MemStore) standardActiveTargetLocked(appID string) bool {
	for _, o := range m.applicationStandardOperations {
		if standardOperationActive(o.State) {
			for _, t := range o.Targets {
				if sameStandardUUID(t.AppID, appID) && t.State != "skipped" {
					return true
				}
			}
		}
	}
	return false
}

func (m *MemStore) ObserveApplicationStandardEnrollment(ctx context.Context, c ApplicationStandardEnrollmentClaim) (ApplicationStandardEnrollment, error) {
	if !standardEnrollmentClaimValid(c) {
		return ApplicationStandardEnrollment{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	key, e, err := m.standardObservationEnrollmentClaimLocked(c, time.Now())
	if err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	if m.standardActiveTargetLocked(c.AppID) {
		return ApplicationStandardEnrollment{}, ErrApplicationStandardOperationInProgress
	}
	q, err := m.qualifyStandardApplicationLocked(ctx, c.OrgID, c.AppID, c.DesiredRevision)
	if err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, _, err := m.standardObservationEnrollmentClaimLocked(c, now); err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	if err := ctx.Err(); err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	e = standardObservedEnrollment(e, q, now)
	m.applicationStandardEnrollments[key] = e
	held := m.applicationStandardEnrollmentClaims[canonicalStandardUUID(c.AppID)]
	held.Owner, held.Until = "", time.Time{}
	m.applicationStandardEnrollmentClaims[canonicalStandardUUID(c.AppID)] = held
	return cloneApplicationStandardEnrollment(e), nil
}

func (m *MemStore) standardObservationEnrollmentClaimLocked(c ApplicationStandardEnrollmentClaim, now time.Time) (string, ApplicationStandardEnrollment, error) {
	held := m.applicationStandardEnrollmentClaims[canonicalStandardUUID(c.AppID)]
	if held.Owner != c.Owner || held.Generation != c.Generation || !held.Until.After(now) {
		return "", ApplicationStandardEnrollment{}, ErrApplicationStandardLeaseLost
	}
	for key, e := range m.applicationStandardEnrollments {
		if sameStandardUUID(e.AppID, c.AppID) && sameStandardUUID(e.OrgID, c.OrgID) && e.DesiredRevision == c.DesiredRevision && standardObservationInstalled(e) {
			return key, e, nil
		}
	}
	return "", ApplicationStandardEnrollment{}, ErrApplicationStandardLeaseLost
}
