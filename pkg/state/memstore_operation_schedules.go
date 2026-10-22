// adr: 20261018161833
package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

func cloneOperationSchedule(s OperationSchedule) OperationSchedule {
	s.Input = append([]byte(nil), s.Input...)
	s.At = cloneTimePtr(s.At)
	s.NextFireAt = cloneTimePtr(s.NextFireAt)
	s.LastFiredAt = cloneTimePtr(s.LastFiredAt)
	return s
}

func (m *MemStore) operationSchedulesLocked() map[string]OperationSchedule {
	data := m.operationMemoryLocked()
	if data.schedules == nil {
		data.schedules = map[string]OperationSchedule{}
	}
	return data.schedules
}

// operationScheduleGoneLocked mirrors the Postgres cascades: a schedule whose
// definition or customer was deleted no longer exists.
func (m *MemStore) operationScheduleGoneLocked(s OperationSchedule) bool {
	def, ok := m.operationMemoryLocked().definitions[s.DefinitionID]
	if !ok || def.AccountID != s.AccountID {
		return true
	}
	tenant, ok := m.platformTenants[s.PlatformTenantID]
	return !ok || tenant.AccountID != s.AccountID
}

func (m *MemStore) operationScheduleLocked(accountID, tenantID, id string) (OperationSchedule, bool) {
	s, ok := m.operationSchedulesLocked()[id]
	if !ok || s.AccountID != accountID || (tenantID != "" && s.PlatformTenantID != tenantID) || m.operationScheduleGoneLocked(s) {
		return OperationSchedule{}, false
	}
	return s, true
}

func (m *MemStore) CreateOperationSchedule(_ context.Context, in OperationScheduleInput) (OperationSchedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	acct, ok := m.accounts[in.AccountID]
	if !ok {
		return OperationSchedule{}, ErrNotFound
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.Operations.Allowed || limits.Operations.SchedulesPerAccount <= 0 {
		return OperationSchedule{}, NewOperationLimitError("schedules_per_account", 0, 1)
	}
	now := in.Now.UTC().Truncate(time.Microsecond)
	kind, timezone, first, err := validateOperationScheduleRequest(in.Request, limits.Operations, now)
	if err != nil {
		return OperationSchedule{}, err
	}
	def, ok := m.operationMemoryLocked().definitions[in.Request.DefinitionID]
	if !ok || def.AccountID != in.AccountID {
		return OperationSchedule{}, ErrNotFound
	}
	if err := validateNewOperationInput(def, canonicalOperationScheduleInput(in.Request.Input), limits); err != nil {
		return OperationSchedule{}, err
	}
	tenant, ok := m.platformTenants[in.PlatformTenantID]
	if !ok || tenant.AccountID != in.AccountID {
		return OperationSchedule{}, ErrNotFound
	}
	if tenant.Status != PlatformTenantActive {
		return OperationSchedule{}, ErrPlatformTenantSuspended
	}
	live := 0
	for _, s := range m.operationSchedulesLocked() {
		if s.AccountID == in.AccountID && (s.State == api.OperationScheduleActive || s.State == api.OperationSchedulePaused) && !m.operationScheduleGoneLocked(s) {
			live++
		}
	}
	if live >= limits.Operations.SchedulesPerAccount {
		return OperationSchedule{}, NewOperationLimitError("schedules_per_account", int64(limits.Operations.SchedulesPerAccount), int64(live)+1)
	}
	s := OperationSchedule{AccountID: in.AccountID, Input: canonicalOperationScheduleInput(in.Request.Input)}
	s.OperationScheduleResponse = api.OperationScheduleResponse{ID: uuid.NewString(), AppID: def.AppID, PlatformTenantID: in.PlatformTenantID, DefinitionID: def.ID,
		Kind: kind, Cron: in.Request.Cron, Timezone: timezone, State: api.OperationScheduleActive, NextFireAt: &first, CreatedAt: now, UpdatedAt: now}
	if kind == api.OperationScheduleOnce {
		at := first
		s.At = &at
	}
	m.operationSchedulesLocked()[s.ID] = cloneOperationSchedule(s)
	return cloneOperationSchedule(s), nil
}

func (m *MemStore) OperationSchedule(_ context.Context, accountID, tenantID, id string) (OperationSchedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.operationScheduleLocked(accountID, tenantID, id)
	if !ok {
		return OperationSchedule{}, ErrNotFound
	}
	return cloneOperationSchedule(s), nil
}

func (m *MemStore) ListOperationSchedules(_ context.Context, accountID, tenantID, appID string) ([]OperationSchedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []OperationSchedule{}
	for _, s := range m.operationSchedulesLocked() {
		if s.AccountID != accountID || (tenantID != "" && s.PlatformTenantID != tenantID) || (appID != "" && s.AppID != appID) || m.operationScheduleGoneLocked(s) {
			continue
		}
		out = append(out, cloneOperationSchedule(s))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if len(out) > operationScheduleListMax {
		out = out[:operationScheduleListMax]
	}
	return out, nil
}

func (m *MemStore) ChangeOperationSchedule(_ context.Context, accountID, tenantID, id, action string, now time.Time) (OperationSchedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.operationScheduleLocked(accountID, tenantID, id)
	if !ok {
		return OperationSchedule{}, ErrNotFound
	}
	state, next, err := operationScheduleChange(s, action, now.UTC())
	if err != nil {
		return OperationSchedule{}, err
	}
	s.State, s.NextFireAt, s.LeaseToken, s.UpdatedAt = state, next, "", now.UTC()
	m.operationSchedulesLocked()[s.ID] = cloneOperationSchedule(s)
	return cloneOperationSchedule(s), nil
}

func (m *MemStore) ClaimDueOperationSchedules(_ context.Context, now time.Time, limit int, lease time.Duration) ([]OperationSchedule, error) {
	if limit <= 0 || lease <= 0 {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	if data.scheduleLeases == nil {
		data.scheduleLeases = map[string]time.Time{}
	}
	due := []OperationSchedule{}
	for id, s := range m.operationSchedulesLocked() {
		if m.operationScheduleGoneLocked(s) {
			delete(m.operationSchedulesLocked(), id)
			continue
		}
		if s.State != api.OperationScheduleActive || s.NextFireAt == nil || s.NextFireAt.After(now) {
			continue
		}
		if until, held := data.scheduleLeases[id]; held && !until.Before(now) {
			continue
		}
		due = append(due, s)
	}
	sort.Slice(due, func(i, j int) bool {
		if !due[i].NextFireAt.Equal(*due[j].NextFireAt) {
			return due[i].NextFireAt.Before(*due[j].NextFireAt)
		}
		return due[i].ID < due[j].ID
	})
	if len(due) > limit {
		due = due[:limit]
	}
	token := uuid.NewString()
	for i := range due {
		due[i].LeaseToken = token
		data.scheduleLeases[due[i].ID] = now.Add(lease)
		m.operationSchedulesLocked()[due[i].ID] = cloneOperationSchedule(due[i])
		due[i] = cloneOperationSchedule(due[i])
	}
	return due, nil
}

func (m *MemStore) FireOperationSchedule(_ context.Context, fire OperationScheduleFire) (OperationSchedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.operationSchedulesLocked()[fire.Schedule.ID]
	if !ok || s.LeaseToken == "" || s.LeaseToken != fire.Schedule.LeaseToken || s.State != api.OperationScheduleActive || m.operationScheduleGoneLocked(s) {
		return OperationSchedule{}, ErrNotFound
	}
	now := fire.Now.UTC().Truncate(time.Microsecond)
	fireAt := *s.NextFireAt
	refusal := fire.Refusal
	if refusal == "" {
		admission := OperationAdmission{AccountID: s.AccountID, DefinitionID: s.DefinitionID, PlatformTenantID: s.PlatformTenantID,
			IdempotencyKey: operationScheduleIdempotencyKey(s.ID, fireAt), Input: s.Input}
		op, _, err := m.admitOperationLocked(admission, nil)
		if reason, refused := operationScheduleRefusal(err); refused {
			refusal = reason
		} else if err != nil {
			return OperationSchedule{}, fmt.Errorf("state: fire operation schedule: %w", err)
		} else {
			s.LastOperationID = op.ID
			s.Fires++
		}
	}
	state, next, err := operationScheduleAfterFire(s, refusal == "", now)
	if err != nil {
		return OperationSchedule{}, err
	}
	s.State, s.NextFireAt, s.LastFiredAt, s.LastError, s.LeaseToken, s.UpdatedAt = state, next, &fireAt, truncateOperationScheduleError(refusal), "", now
	delete(m.operationMemoryLocked().scheduleLeases, s.ID)
	m.operationSchedulesLocked()[s.ID] = cloneOperationSchedule(s)
	return cloneOperationSchedule(s), nil
}
