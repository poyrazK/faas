package state

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ApplicationStandardLogHealthStore = (*MemStore)(nil)

func (m *MemStore) RecordApplicationStandardLogHealth(ctx context.Context, s ApplicationStandardLogConsumerSession, d AppLogDrain, e ApplicationStandardLogHealthEvent) (ApplicationStandardLogHealthObservation, error) {
	if !validStandardLogHealth(s, d, e) {
		return ApplicationStandardLogHealthObservation{}, ErrInvalidArgument
	}
	s = canonicalStandardLogHealthSession(s)
	if e.SourceInstanceID != "" {
		e.SourceInstanceID = canonicalStandardUUID(e.SourceInstanceID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ApplicationStandardLogHealthObservation{}, err
	}
	if !m.standardLogSessionCurrentLocked(s) || !m.standardLogNodeEligibleLocked(s.NodeID) {
		return ApplicationStandardLogHealthObservation{}, ErrApplicationStandardLogConsumerFenced
	}
	if !m.standardLogHealthProjectionLocked(d, e) {
		return ApplicationStandardLogHealthObservation{}, ErrApplicationStandardLogDeliveryStale
	}
	now, err := m.standardLogDeliveryClockLocked(ctx, d)
	if err != nil {
		return ApplicationStandardLogHealthObservation{}, err
	}
	now = now.UTC().Truncate(time.Microsecond)
	o := ApplicationStandardLogHealthObservation{ApplicationStandardLogDrainBinding: *d.StandardBinding, ApplicationStandardLogConsumerSession: s, ApplicationStandardLogHealthEvent: e, EventAt: now, ObservedAt: now}
	key := standardLogHealthKey(o.AppID, o.DrainID, o.NodeID)
	old := m.applicationStandardLogHealth[key]
	if old.ApplicationStandardLogDrainBinding == o.ApplicationStandardLogDrainBinding && old.ApplicationStandardLogConsumerSession == s {
		if e.EventRevision < old.EventRevision || e.EventRevision == old.EventRevision && e != old.ApplicationStandardLogHealthEvent {
			return ApplicationStandardLogHealthObservation{}, ErrApplicationStandardLogHealthStale
		}
		if e.EventRevision == old.EventRevision {
			o.EventAt = old.EventAt
		}
	}
	if m.applicationStandardLogHealth == nil {
		m.applicationStandardLogHealth = map[string]ApplicationStandardLogHealthObservation{}
	}
	m.applicationStandardLogHealth[key] = o
	return o, nil
}

func (m *MemStore) standardLogHealthProjectionLocked(d AppLogDrain, e ApplicationStandardLogHealthEvent) bool {
	for _, current := range m.appLogDrains {
		if !sameStandardUUID(current.ID, d.ID) {
			continue
		}
		if !sameStandardLogBinding(m.standardLogBindingLocked(current), d.StandardBinding) {
			return false
		}
		if e.Status != "healthy" {
			return true
		}
		for _, ins := range m.instances {
			if sameStandardUUID(ins.ID, e.SourceInstanceID) && sameStandardUUID(ins.AppID, d.AppID) {
				return true
			}
		}
		return false
	}
	return false
}

func (m *MemStore) ListApplicationStandardLogHealth(ctx context.Context, orgID, appID string) ([]ApplicationStandardLogHealthObservation, error) {
	if !validStandardResourceRead(orgID, appID) {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !m.standardLogInventoryReadScopeLocked(orgID, appID) {
		return nil, ErrNotFound
	}
	rows := []ApplicationStandardLogHealthObservation{}
	for _, o := range m.applicationStandardLogHealth {
		if !sameStandardUUID(o.OrgID, orgID) || !sameStandardUUID(o.AppID, appID) || !m.standardLogSessionCurrentLocked(o.ApplicationStandardLogConsumerSession) || !m.standardLogNodeEligibleLocked(o.NodeID) || time.Since(o.ObservedAt) >= api.ApplicationStandardLogHealthFreshness {
			continue
		}
		d := AppLogDrain{ID: o.DrainID, StandardBinding: &o.ApplicationStandardLogDrainBinding, AppID: o.AppID}
		if m.standardLogHealthProjectionLocked(d, o.ApplicationStandardLogHealthEvent) {
			rows = append(rows, o)
		}
	}
	slices.SortFunc(rows, func(a, b ApplicationStandardLogHealthObservation) int {
		return strings.Compare(a.NodeID+"/"+a.DrainID, b.NodeID+"/"+b.DrainID)
	})
	return rows, nil
}

func (m *MemStore) eraseStandardLogHealthLocked(app, drain, node string) {
	for key, o := range m.applicationStandardLogHealth {
		if app != "" && sameStandardUUID(o.AppID, app) || drain != "" && sameStandardUUID(o.DrainID, drain) || node != "" && sameStandardUUID(o.NodeID, node) {
			delete(m.applicationStandardLogHealth, key)
		}
	}
}
