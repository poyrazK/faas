package state

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

var _ ApplicationStandardMaterializationStore = (*MemStore)(nil)

func (m *MemStore) ClaimApplicationStandardOperation(ctx context.Context, owner string) (ApplicationStandardWorkerClaim, error) {
	if !standardWorkerOwnerValid(owner) {
		return ApplicationStandardWorkerClaim{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return ApplicationStandardWorkerClaim{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC().Truncate(time.Microsecond)
	ids := []string{}
	for id, o := range m.applicationStandardOperations {
		if o.State != "queued" && o.State != "running" && o.State != "waiting" {
			continue
		}
		if c := m.applicationStandardWorkerClaims[id]; c.Owner != "" && c.Until.After(now) {
			continue
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := m.applicationStandardOperations[ids[i]], m.applicationStandardOperations[ids[j]]
		if !a.UpdatedAt.Equal(b.UpdatedAt) {
			return a.UpdatedAt.Before(b.UpdatedAt)
		}
		if a.CreatedAt.Equal(b.CreatedAt) {
			return a.ID < b.ID
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
	if len(ids) == 0 {
		return ApplicationStandardWorkerClaim{}, ErrNotFound
	}
	id := ids[0]
	old := m.applicationStandardWorkerClaims[id]
	c := ApplicationStandardWorkerClaim{OperationID: id, OrgID: m.applicationStandardOperations[id].OrgID, Owner: owner, Generation: old.Generation + 1, Until: now.Add(api.ApplicationStandardWorkerLease)}
	if m.applicationStandardWorkerClaims == nil {
		m.applicationStandardWorkerClaims = map[string]ApplicationStandardWorkerClaim{}
	}
	m.applicationStandardWorkerClaims[id] = c
	o := m.applicationStandardOperations[id]
	o.UpdatedAt = now
	m.applicationStandardOperations[id] = o
	return c, nil
}

func (m *MemStore) MaterializeNextApplicationStandardTarget(ctx context.Context, c ApplicationStandardWorkerClaim) (ApplicationStandardOperation, error) {
	if !standardWorkerClaimValid(c) {
		return ApplicationStandardOperation{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return ApplicationStandardOperation{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC().Truncate(time.Microsecond)
	held := m.applicationStandardWorkerClaims[canonicalStandardUUID(c.OperationID)]
	o, exists := m.applicationStandardOperations[canonicalStandardUUID(c.OperationID)]
	if !exists || !sameStandardUUID(o.OrgID, c.OrgID) {
		return ApplicationStandardOperation{}, ErrNotFound
	}
	if held.Owner != c.Owner || held.Generation != c.Generation || !held.Until.After(now) || o.State == "paused" || !standardOperationActive(o.State) {
		return ApplicationStandardOperation{}, ErrApplicationStandardLeaseLost
	}
	o = cloneStandardOperation(o)
	index := standardNextTarget(o)
	if index < 0 {
		return m.standardMaterializationCheckpointLocked(c, o, now), nil
	}
	t := &o.Targets[index]
	plan, err := m.standardReviewPlanLocked(o.OrgID, o.PlanID)
	if err != nil {
		return ApplicationStandardOperation{}, err
	}
	r := plan.Request
	r.Scope, r.ScopeID = "application", t.AppID
	snapshot, err := m.standardReviewSnapshotLocked(ctx, o.OrgID, o.ApprovedBy, r)
	if err != nil {
		return ApplicationStandardOperation{}, err
	}
	app, err := standardMaterializationInput(snapshot, o, *t, plan.Request, now)
	if errors.Is(err, ErrApplicationStandardReviewStale) || errors.Is(err, ErrApplicationStandardReviewBlocked) {
		t.State, t.ErrorCode, t.UpdatedAt = "blocked", "reviewed_inputs_changed", now
		return m.standardMaterializationCheckpointLocked(c, o, now), nil
	}
	if err != nil {
		return ApplicationStandardOperation{}, err
	}
	drains := listAppLogDrains(func(d AppLogDrain) bool { return sameStandardUUID(d.AppID, t.AppID) }, m.appLogDrains)
	signers := []AppTrustedSigner{}
	for _, signer := range m.trustedSigners {
		if sameStandardUUID(signer.AppID, t.AppID) {
			signers = append(signers, signer)
		}
	}
	bindings, backups := []standardControlBinding{}, []standardControlBackup{}
	for _, b := range m.applicationStandardControlBindings {
		if sameStandardUUID(b.AppID, t.AppID) {
			bindings = append(bindings, b)
		}
	}
	for _, b := range m.applicationStandardControlBackups {
		if sameStandardUUID(b.AppID, t.AppID) {
			backups = append(backups, b)
		}
	}
	projection, err := buildStandardControlProjection(app, t.ApprovedApp, drains, signers, bindings, backups, m.applicationStandardLogDestinations, m.applicationStandardPublishers, now)
	if errors.Is(err, ErrConflict) || errors.Is(err, ErrApplicationStandardReviewBlocked) {
		t.State, t.ErrorCode, t.UpdatedAt = "blocked", "control_projection_conflict", now
		return m.standardMaterializationCheckpointLocked(c, o, now), nil
	}
	if err != nil {
		return ApplicationStandardOperation{}, err
	}
	if err := ctx.Err(); err != nil {
		return ApplicationStandardOperation{}, err
	}
	enrollment := standardInstalledEnrollment(app, *t, now)
	m.installStandardProjectionLocked(app, projection, enrollment)

	t.State, t.DesiredRevision, t.ErrorCode, t.UpdatedAt = "persisted", enrollment.DesiredRevision, "", now
	return m.standardMaterializationCheckpointLocked(c, o, now), nil
}

func (m *MemStore) standardMaterializationCheckpointLocked(c ApplicationStandardWorkerClaim, o ApplicationStandardOperation, now time.Time) ApplicationStandardOperation {
	o.State, o.UpdatedAt = standardProjectionOperationState(o), now
	m.applicationStandardOperations[o.ID] = cloneStandardOperation(o)
	if o.State != "running" {
		held := m.applicationStandardWorkerClaims[o.ID]
		held.Owner, held.Until = "", time.Time{}
		m.applicationStandardWorkerClaims[o.ID] = held
	}
	return cloneStandardOperation(o)
}

func (m *MemStore) standardManagedControlLocked(appID string, field appstandards.Field) bool {
	for _, e := range m.applicationStandardEnrollments {
		if sameStandardUUID(e.AppID, appID) {
			return standardManagedField(e, field)
		}
	}
	return false
}

// Match the app foreign-key cascades for private restoration material.
func (m *MemStore) deleteStandardMaterializationControlsLocked(appID string) {
	m.eraseStandardLogDeliveriesLocked(appID)
	m.eraseStandardLogInventoriesLocked(appID)
	delete(m.applicationStandardEnrollmentClaims, canonicalStandardUUID(appID))
	for key, b := range m.applicationStandardControlBindings {
		if sameStandardUUID(b.AppID, appID) {
			delete(m.applicationStandardControlBindings, key)
		}
	}
	for key, b := range m.applicationStandardControlBackups {
		if sameStandardUUID(b.AppID, appID) {
			delete(m.applicationStandardControlBackups, key)
		}
	}
}

func (m *MemStore) installStandardProjectionLocked(app standardReviewAppSnapshot, projection standardControlProjection, enrollment ApplicationStandardEnrollment) {
	// All validation precedes mutations; this critical section is the MemStore
	// equivalent of the PostgreSQL installation/checkpoint transaction.
	if m.applicationStandardControlBackups == nil {
		m.applicationStandardControlBackups = map[string]standardControlBackup{}
	}
	for _, b := range projection.Backups {
		m.applicationStandardControlBackups[standardBindingKey(b.AppID, b.Field, b.ID)] = b
	}
	if m.applicationStandardControlBindings == nil {
		m.applicationStandardControlBindings = map[string]standardControlBinding{}
	}
	for key, b := range m.applicationStandardControlBindings {
		if sameStandardUUID(b.AppID, app.AppID) {
			delete(m.applicationStandardControlBindings, key)
		}
	}
	for _, b := range projection.Bindings {
		m.applicationStandardControlBindings[standardBindingKey(b.AppID, b.Field, b.ResourceID)] = b
	}
	physicalAppID, physicalAccountID := app.AppID, app.AccountID
	for _, actual := range m.apps {
		if sameStandardUUID(actual.ID, app.AppID) {
			physicalAppID, physicalAccountID = actual.ID, actual.AccountID
			break
		}
	}
	for i := range projection.Drains {
		projection.Drains[i].AppID, projection.Drains[i].AccountID = physicalAppID, physicalAccountID
	}
	for i := range projection.Signers {
		projection.Signers[i].AppID, projection.Signers[i].AccountID = physicalAppID, physicalAccountID
	}
	selectedDrains := map[string]bool{}
	for _, d := range projection.Drains {
		selectedDrains[d.ID] = true
		m.appLogDrains[d.ID] = cloneAppLogDrain(d)
	}
	for _, d := range m.appLogDrains {
		if sameStandardUUID(d.AppID, app.AppID) && !selectedDrains[d.ID] {
			delete(m.appLogDrains, d.ID)
			m.eraseStandardLogDrainDeliveriesLocked(d.ID)
			delete(m.appLogDrainHealth, d.ID)
			for key, sample := range m.appLogDrainAnalytics {
				if sample.DrainID == d.ID {
					delete(m.appLogDrainAnalytics, key)
				}
			}
		}
	}
	for key, signer := range m.trustedSigners {
		if sameStandardUUID(signer.AppID, app.AppID) {
			delete(m.trustedSigners, key)
		}
	}
	for _, signer := range projection.Signers {
		m.trustedSigners[trustedSignerKey{AppID: signer.AppID, SignerName: signer.SignerName}] = signer
	}
	for key, actual := range m.apps {
		if !sameStandardUUID(actual.ID, app.AppID) {
			continue
		}
		actual.RequireSigned, actual.SecurityPolicy = projection.RequireSigned, projection.SecurityPolicy
		before := actual
		actual.EgressAllowlist, actual.EgressPorts = projection.CIDRs, projection.Ports
		m.advanceAppEgressRevisionLocked(before, actual)
		m.apps[key] = actual
	}
	enrollmentKey := app.AppID
	for key, existing := range m.applicationStandardEnrollments {
		if sameStandardUUID(existing.AppID, app.AppID) {
			enrollmentKey = key
			break
		}
	}
	m.applicationStandardEnrollments[enrollmentKey] = cloneApplicationStandardEnrollment(enrollment)
	m.revokeStandardEnrollmentClaimLocked(app.AppID)
}

func (m *MemStore) revokeStandardEnrollmentClaimLocked(appID string) {
	if m.applicationStandardEnrollmentClaims == nil {
		m.applicationStandardEnrollmentClaims = map[string]ApplicationStandardEnrollmentClaim{}
	}
	id := canonicalStandardUUID(appID)
	c := m.applicationStandardEnrollmentClaims[id]
	c.Generation++
	c.Owner, c.Until = "", time.Time{}
	m.applicationStandardEnrollmentClaims[id] = c
}
