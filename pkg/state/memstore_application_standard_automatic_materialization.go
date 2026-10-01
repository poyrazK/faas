package state

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ApplicationStandardAutomaticMaterializationStore = (*MemStore)(nil)

func (m *MemStore) standardQueuedTargetLocked(appID string) bool {
	for _, o := range m.applicationStandardOperations {
		if !standardOperationActive(o.State) {
			continue
		}
		for _, t := range o.Targets {
			if sameStandardUUID(t.AppID, appID) && (t.State == "queued" || t.State == "applying") {
				return true
			}
		}
	}
	return false
}

func (m *MemStore) ClaimApplicationStandardEnrollment(ctx context.Context, owner string) (ApplicationStandardEnrollmentClaim, error) {
	if !standardWorkerOwnerValid(owner) {
		return ApplicationStandardEnrollmentClaim{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return ApplicationStandardEnrollmentClaim{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC().Truncate(time.Microsecond)
	entries := []ApplicationStandardEnrollment{}
	for _, e := range m.applicationStandardEnrollments {
		if e.State != "pending" && !(e.State == "blocked" && !e.UpdatedAt.Add(api.ApplicationStandardBlockedRetry).After(now)) {
			continue
		}
		held := m.applicationStandardEnrollmentClaims[canonicalStandardUUID(e.AppID)]
		if held.Owner != "" && held.Until.After(now) || m.standardQueuedTargetLocked(e.AppID) {
			continue
		}
		for _, a := range m.apps {
			if sameStandardUUID(a.ID, e.AppID) && a.Status != AppDeleted {
				entries = append(entries, e)
				break
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].UpdatedAt.Equal(entries[j].UpdatedAt) {
			return entries[i].AppID < entries[j].AppID
		}
		return entries[i].UpdatedAt.Before(entries[j].UpdatedAt)
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
	m.applicationStandardEnrollmentClaims[id] = c
	return c, nil
}

func (m *MemStore) MaterializeApplicationStandardEnrollment(ctx context.Context, c ApplicationStandardEnrollmentClaim) (ApplicationStandardEnrollment, error) {
	if !standardEnrollmentClaimValid(c) {
		return ApplicationStandardEnrollment{}, ErrInvalidArgument
	}
	c.AppID, c.OrgID = canonicalStandardUUID(c.AppID), canonicalStandardUUID(c.OrgID)
	if err := ctx.Err(); err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC().Truncate(time.Microsecond)
	held := m.applicationStandardEnrollmentClaims[canonicalStandardUUID(c.AppID)]
	key := ""
	var current ApplicationStandardEnrollment
	for k, e := range m.applicationStandardEnrollments {
		if sameStandardUUID(e.AppID, c.AppID) {
			key, current = k, e
			break
		}
	}
	if key == "" || !sameStandardUUID(current.OrgID, c.OrgID) || current.DesiredRevision != c.DesiredRevision || held.Owner != c.Owner || held.Generation != c.Generation || !held.Until.After(now) || (current.State != "pending" && current.State != "blocked") {
		return ApplicationStandardEnrollment{}, ErrApplicationStandardLeaseLost
	}
	if m.standardQueuedTargetLocked(c.AppID) {
		held.Owner, held.Until = "", time.Time{}
		m.applicationStandardEnrollmentClaims[c.AppID] = held
		return cloneApplicationStandardEnrollment(current), ErrApplicationStandardOperationInProgress
	}
	r := ApplicationStandardReviewRequest{Scope: "application", ScopeID: c.AppID, StandardID: "00000000-0000-0000-0000-000000000000"}
	snapshot, err := m.standardReviewSnapshotLocked(c.OrgID, c.OrgID, r)
	if err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	app, proposed, code, err := resolveAutomaticStandardEnrollment(snapshot, now)
	if err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	if code != "" {
		return m.blockStandardEnrollmentLocked(key, c, current, code, now), nil
	}
	drains, signers, bindings, backups := m.standardPhysicalControlsLocked(c.AppID)
	projection, err := buildStandardControlProjection(app, proposed, drains, signers, bindings, backups, m.applicationStandardLogDestinations, m.applicationStandardPublishers, now)
	if errors.Is(err, ErrConflict) || errors.Is(err, ErrApplicationStandardReviewBlocked) {
		return m.blockStandardEnrollmentLocked(key, c, current, "control_projection_conflict", now), nil
	}
	if err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	e, err := automaticInstalledStandardEnrollment(app, proposed, now)
	if err != nil {
		return e, err
	}
	if err := ctx.Err(); err != nil {
		return ApplicationStandardEnrollment{}, err
	}
	if !held.Until.After(time.Now()) {
		return ApplicationStandardEnrollment{}, ErrApplicationStandardLeaseLost
	}
	m.installStandardProjectionLocked(app, projection, e)
	return cloneApplicationStandardEnrollment(e), nil
}

func (m *MemStore) blockStandardEnrollmentLocked(key string, c ApplicationStandardEnrollmentClaim, e ApplicationStandardEnrollment, code string, now time.Time) ApplicationStandardEnrollment {
	e.State, e.ErrorCode, e.UpdatedAt = "blocked", code, now
	m.applicationStandardEnrollments[key] = cloneApplicationStandardEnrollment(e)
	held := m.applicationStandardEnrollmentClaims[c.AppID]
	held.Owner, held.Until = "", time.Time{}
	m.applicationStandardEnrollmentClaims[c.AppID] = held
	return cloneApplicationStandardEnrollment(e)
}

func (m *MemStore) standardPhysicalControlsLocked(appID string) ([]AppLogDrain, []AppTrustedSigner, []standardControlBinding, []standardControlBackup) {
	drains := listAppLogDrains(func(d AppLogDrain) bool { return sameStandardUUID(d.AppID, appID) }, m.appLogDrains)
	signers, bindings, backups := []AppTrustedSigner{}, []standardControlBinding{}, []standardControlBackup{}
	for _, s := range m.trustedSigners {
		if sameStandardUUID(s.AppID, appID) {
			signers = append(signers, s)
		}
	}
	for _, b := range m.applicationStandardControlBindings {
		if sameStandardUUID(b.AppID, appID) {
			bindings = append(bindings, b)
		}
	}
	for _, b := range m.applicationStandardControlBackups {
		if sameStandardUUID(b.AppID, appID) {
			backups = append(backups, b)
		}
	}
	return drains, signers, bindings, backups
}

func (m *MemStore) ReleaseApplicationStandardOperationWorker(ctx context.Context, c ApplicationStandardWorkerClaim) error {
	if !standardWorkerClaimValid(c) {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	held := m.applicationStandardWorkerClaims[canonicalStandardUUID(c.OperationID)]
	if held.Owner != c.Owner || held.Generation != c.Generation || !sameStandardUUID(held.OrgID, c.OrgID) {
		return ErrApplicationStandardLeaseLost
	}
	held.Owner, held.Until = "", time.Time{}
	m.applicationStandardWorkerClaims[canonicalStandardUUID(c.OperationID)] = held
	return nil
}
