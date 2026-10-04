package state

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/appstandards"
)

var _ ApplicationStandardLogDeliveryStore = (*MemStore)(nil)

func (m *MemStore) standardLogParentsLocked(d AppLogDrain) (App, ApplicationStandardEnrollment, bool) {
	var app App
	var e ApplicationStandardEnrollment
	for _, a := range m.apps {
		if sameStandardUUID(a.ID, d.AppID) {
			app = a
			break
		}
	}
	for _, row := range m.applicationStandardEnrollments {
		if sameStandardUUID(row.AppID, d.AppID) {
			e = row
			break
		}
	}
	if app.Status != AppActive || !sameStandardUUID(app.AccountID, d.AccountID) || !applicationStandardEnrollmentPermitsRuntimeAt(app, e, time.Now()) {
		return app, e, false
	}
	orgActive, accountActive := false, false
	for _, o := range m.orgs {
		if sameStandardUUID(o.ID, app.OrgID) {
			orgActive = o.Status == OrgStatusActive && !o.DeletedPending
			break
		}
	}
	for _, a := range m.accounts {
		if sameStandardUUID(a.ID, app.AccountID) {
			accountActive = a.Status == AccountActive
			break
		}
	}
	return app, e, orgActive && accountActive
}

func (m *MemStore) standardLogBindingLocked(d AppLogDrain) *ApplicationStandardLogDrainBinding {
	if !d.Enabled {
		return nil
	}
	_, e, ok := m.standardLogParentsLocked(d)
	if !ok || len(e.Effective.Sources[appstandards.LogDestinations]) == 0 {
		return nil
	}
	for _, b := range m.applicationStandardControlBindings {
		if b.Field != appstandards.LogDestinations || !sameStandardUUID(b.AppID, d.AppID) || !sameStandardUUID(b.PhysicalID, d.ID) {
			continue
		}
		r, ok := m.applicationStandardLogDestinations[canonicalStandardUUID(b.ResourceID)]
		if !ok {
			return nil
		}
		return standardLogBindingFromCurrent(d, e, b, r)
	}
	return nil
}

func (m *MemStore) RecordApplicationStandardLogDelivery(ctx context.Context, d AppLogDrain, source string, seq uint64) (ApplicationStandardLogDeliveryObservation, error) {
	if !validStandardLogDelivery(d, source, seq) {
		return ApplicationStandardLogDeliveryObservation{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return ApplicationStandardLogDeliveryObservation{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var current AppLogDrain
	for _, row := range m.appLogDrains {
		if sameStandardUUID(row.ID, d.ID) {
			current = row
			break
		}
	}
	if ApplicationStandardLogDrainConfigHash(current) != d.StandardBinding.DrainConfigHash || !sameStandardLogBinding(m.standardLogBindingLocked(current), d.StandardBinding) {
		return ApplicationStandardLogDeliveryObservation{}, ErrApplicationStandardLogDeliveryStale
	}
	owned := false
	for _, ins := range m.instances {
		if sameStandardUUID(ins.ID, source) && sameStandardUUID(ins.AppID, d.AppID) {
			owned = true
			break
		}
	}
	if !owned {
		return ApplicationStandardLogDeliveryObservation{}, ErrApplicationStandardLogDeliveryStale
	}
	now, err := m.standardLogDeliveryClockLocked(ctx, current)
	if err != nil {
		return ApplicationStandardLogDeliveryObservation{}, err
	}
	result := standardLogDeliveryObservation(d, source, seq, now)
	if m.applicationStandardLogDeliveries == nil {
		m.applicationStandardLogDeliveries = map[string]ApplicationStandardLogDeliveryObservation{}
	}
	m.applicationStandardLogDeliveries[standardLogDeliveryKey(result.ApplicationStandardLogDrainBinding)] = result
	return result, nil
}

func (m *MemStore) standardLogDeliveryClockLocked(ctx context.Context, current AppLogDrain) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	_, e, eligible := m.standardLogParentsLocked(current)
	now := time.Now()
	if !eligible || e.ExceptionExpiresAt != nil && !now.Before(*e.ExceptionExpiresAt) {
		return time.Time{}, ErrApplicationStandardLogDeliveryStale
	}
	return now, nil
}

func (m *MemStore) ListApplicationStandardLogDeliveries(ctx context.Context, orgID, appID string) ([]ApplicationStandardLogDeliveryObservation, error) {
	if !validStandardResourceRead(orgID, appID) {
		return nil, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	owned := false
	for _, a := range m.apps {
		if sameStandardUUID(a.ID, appID) && sameStandardUUID(a.OrgID, orgID) && a.Status != AppDeleted {
			owned = true
			break
		}
	}
	if !owned {
		return nil, ErrNotFound
	}
	result := []ApplicationStandardLogDeliveryObservation{}
	for _, r := range m.applicationStandardLogDeliveries {
		if sameStandardUUID(r.OrgID, orgID) && sameStandardUUID(r.AppID, appID) {
			result = append(result, r)
		}
	}
	slices.SortFunc(result, func(a, b ApplicationStandardLogDeliveryObservation) int {
		return strings.Compare(a.ResourceID, b.ResourceID)
	})
	return result, nil
}

func (m *MemStore) eraseStandardLogDeliveriesLocked(appID string) {
	for key, r := range m.applicationStandardLogDeliveries {
		if sameStandardUUID(r.AppID, appID) {
			delete(m.applicationStandardLogDeliveries, key)
		}
	}
}

func (m *MemStore) eraseStandardLogDrainDeliveriesLocked(drainID string) {
	for key, r := range m.applicationStandardLogDeliveries {
		if sameStandardUUID(r.DrainID, drainID) {
			delete(m.applicationStandardLogDeliveries, key)
		}
	}
}
