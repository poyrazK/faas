package state

import (
	"context"
	"encoding/json"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type memoryCacheSchedule struct {
	Account, Token string
	Lease          time.Time
	Value          api.EnvironmentCacheSchedule
}

func cloneCacheSchedule(in api.EnvironmentCacheSchedule) api.EnvironmentCacheSchedule {
	raw, _ := json.Marshal(in)
	out, _ := decodeCacheSchedule(raw)
	return out
}
func (m *MemStore) CreateEnvironmentCacheSchedule(_ context.Context, account, app, scope string, req api.CreateEnvironmentCacheScheduleRequest) (api.EnvironmentCacheSchedule, error) {
	var out api.EnvironmentCacheSchedule
	if api.ValidateEnvironmentCacheSchedule(req) != nil || req.AppID != app {
		return out, ErrInvalidArgument
	}
	req = normalizeSchedule(req)
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.environmentCachePolicyLocked(account, app, scope)
	if err != nil {
		return out, err
	}
	if !m.accounts[account].MayDeploy() {
		return out, ErrEnvironmentCacheAccount
	}
	if current.ProjectID != req.ProjectID || current.EnvironmentID != req.EnvironmentID {
		return out, ErrRouteHealthRevision
	}
	if old, ok := m.environmentCacheSchedules[req.ScheduleID]; ok {
		if old.Account != account || !reflect.DeepEqual(old.Value.CreateEnvironmentCacheScheduleRequest, req) {
			return out, ErrConflict
		}
		return cloneCacheSchedule(old.Value), nil
	}
	var count int64
	for _, v := range m.environmentCacheSchedules {
		if v.Account == account && v.Value.AppID == app && v.Value.EnvironmentID == req.EnvironmentID && (v.Value.Status == "scheduled" || v.Value.Status == "executing") {
			count++
		}
	}
	if err = scheduleAdmission(req, current, count, time.Now()); err != nil {
		return out, err
	}
	if _, ok := m.environmentCacheReceipts[account+"\x00"+req.ScheduleID]; ok {
		return out, ErrConflict
	}
	if _, ok := m.environmentCachePurges[account+"/"+req.ScheduleID]; ok {
		return out, ErrConflict
	}
	out = api.EnvironmentCacheSchedule{CreateEnvironmentCacheScheduleRequest: req, Scope: scope, Status: "scheduled", CreatedAt: time.Now().UTC()}
	if m.environmentCacheSchedules == nil {
		m.environmentCacheSchedules = map[string]memoryCacheSchedule{}
	}
	m.environmentCacheSchedules[req.ScheduleID] = memoryCacheSchedule{Account: account, Value: out}
	m.appendCacheScheduleEventLocked(account, out, "accepted")
	return cloneCacheSchedule(out), nil
}
func (m *MemStore) cacheSchedule(account, app, scope, id string, cancel bool) (api.EnvironmentCacheSchedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out api.EnvironmentCacheSchedule
	current, err := m.environmentCachePolicyLocked(account, app, scope)
	if err != nil {
		return out, err
	}
	v, ok := m.environmentCacheSchedules[id]
	if !ok || v.Account != account || v.Value.AppID != app || v.Value.EnvironmentID != current.EnvironmentID {
		return out, ErrNotFound
	}
	if cancel {
		if v.Value.Status == "scheduled" {
			now := time.Now().UTC()
			v.Value.Status = "cancelled"
			v.Value.FinishedAt = &now
			m.environmentCacheSchedules[id] = v
			m.appendCacheScheduleEventLocked(account, v.Value, "cancelled")
		}
		if v.Value.Status != "cancelled" {
			return out, ErrEnvironmentCacheScheduleClaimed
		}
	}
	return cloneCacheSchedule(v.Value), nil
}
func (m *MemStore) GetEnvironmentCacheSchedule(_ context.Context, account, app, scope, id string) (api.EnvironmentCacheSchedule, error) {
	return m.cacheSchedule(account, app, scope, id, false)
}
func (m *MemStore) CancelEnvironmentCacheSchedule(_ context.Context, account, app, scope, id string) (api.EnvironmentCacheSchedule, error) {
	return m.cacheSchedule(account, app, scope, id, true)
}
func (m *MemStore) ClaimEnvironmentCacheSchedule(_ context.Context) (EnvironmentCacheScheduleClaim, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	var id string
	for k, v := range m.environmentCacheSchedules {
		if v.Value.ExecuteAt.After(now) || v.Value.Status != "scheduled" && (v.Value.Status != "executing" || v.Lease.After(now)) {
			continue
		}
		if id == "" || v.Value.ExecuteAt.Before(m.environmentCacheSchedules[id].Value.ExecuteAt) || v.Value.ExecuteAt.Equal(m.environmentCacheSchedules[id].Value.ExecuteAt) && k < id {
			id = k
		}
	}
	if id == "" {
		return EnvironmentCacheScheduleClaim{}, ErrNotFound
	}
	v := m.environmentCacheSchedules[id]
	v.Token = uuid.NewString()
	v.Lease = now.Add(time.Minute)
	v.Value.Status = "executing"
	m.environmentCacheSchedules[id] = v
	return EnvironmentCacheScheduleClaim{AccountID: v.Account, LeaseToken: v.Token, Schedule: cloneCacheSchedule(v.Value)}, nil
}
func (m *MemStore) FinishEnvironmentCacheSchedule(_ context.Context, c EnvironmentCacheScheduleClaim, out api.EnvironmentCacheSchedule) error {
	if out.Status != "executed" && out.Status != "failed" {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.environmentCacheSchedules[c.Schedule.ScheduleID]
	if !ok || v.Token != c.LeaseToken || v.Value.Status != "executing" {
		return ErrConflict
	}
	v.Value.Status = out.Status
	v.Value.ErrorCode = out.ErrorCode
	v.Value.PolicyResult = out.PolicyResult
	v.Value.PurgeResult = out.PurgeResult
	now := time.Now().UTC()
	v.Value.FinishedAt = &now
	v.Token = ""
	v.Lease = time.Time{}
	v.Value = cloneCacheSchedule(v.Value)
	m.environmentCacheSchedules[c.Schedule.ScheduleID] = v
	m.appendCacheScheduleEventLocked(c.AccountID, v.Value, v.Value.Status)
	return nil
}

func (m *MemStore) RecoverEnvironmentCacheScheduleAction(_ context.Context, c EnvironmentCacheScheduleClaim) (api.EnvironmentCacheSchedule, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := cloneCacheSchedule(c.Schedule)
	if out.Policy != nil {
		row, ok := m.environmentCacheReceipts[c.AccountID+"\x00"+out.ScheduleID]
		if !ok {
			return out, false, nil
		}
		if row.Hash != environmentCacheHash(out.Scope, *out.Policy) {
			return out, false, ErrConflict
		}
		policy := cloneEnvironmentCachePolicy(row.Response)
		out.PolicyResult = &policy
	} else if out.Purge != nil {
		row, ok := m.environmentCachePurges[c.AccountID+"/"+out.ScheduleID]
		if !ok {
			return out, false, nil
		}
		if !purgeMatches(row, *out.Purge) {
			return out, false, ErrConflict
		}
		out.PurgeResult = &row
	} else {
		return out, false, ErrInvalidArgument
	}
	return out, true, nil
}
