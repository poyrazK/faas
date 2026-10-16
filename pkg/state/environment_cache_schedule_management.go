package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EnvironmentCacheScheduleManagement interface {
	ListEnvironmentCacheSchedules(context.Context, string, string, string, api.EnvironmentCacheScheduleListOptions) (api.EnvironmentCacheScheduleList, error)
	RescheduleEnvironmentCache(context.Context, string, string, string, string, api.RescheduleEnvironmentCacheRequest) (api.EnvironmentCacheSchedule, error)
}

func scheduleListPage(current api.EnvironmentCachePolicy, o api.EnvironmentCacheScheduleListOptions, c api.EnvironmentCacheScheduleCursor, entries []api.EnvironmentCacheSchedule) api.EnvironmentCacheScheduleList {
	out := api.EnvironmentCacheScheduleList{AppID: current.AppID, ProjectID: current.ProjectID, EnvironmentID: current.EnvironmentID, Scope: current.Scope, Entries: entries}
	if len(entries) > o.Limit {
		out.Entries = entries[:o.Limit]
		last := out.Entries[len(out.Entries)-1]
		c.ExecuteAt = last.ExecuteAt
		c.ScheduleID = last.ScheduleID
		out.NextCursor = api.EncodeEnvironmentCacheScheduleCursor(c)
	}
	return out
}
func (s *PgStore) ListEnvironmentCacheSchedules(ctx context.Context, account, app, scope string, o api.EnvironmentCacheScheduleListOptions) (api.EnvironmentCacheScheduleList, error) {
	var out api.EnvironmentCacheScheduleList
	o, err := api.NormalizeEnvironmentCacheScheduleList(o)
	if err != nil {
		return out, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := pgEnvironmentCachePolicy(ctx, tx, account, app, scope, false)
	if err != nil {
		return out, err
	}
	c, err := api.ParseEnvironmentCacheScheduleCursor(o, app, current.ProjectID, current.EnvironmentID)
	if err != nil {
		return out, ErrInvalidArgument
	}
	stamp := func(t *time.Time) pgtype.Timestamptz {
		if t == nil {
			return pgtype.Timestamptz{}
		}
		return pgtype.Timestamptz{Time: *t, Valid: true}
	}
	after := c.ScheduleID
	if after == "" {
		after = uuid.Nil.String()
	}
	rows, err := sqlc.New().ListEnvironmentCacheSchedules(ctx, tx, sqlc.ListEnvironmentCacheSchedulesParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), EnvironmentID: mustPgUUID(current.EnvironmentID), StatusFilter: o.Status, FromTime: stamp(o.From), UntilTime: stamp(o.Until), HasCursor: o.After != "", AfterTime: pgtype.Timestamptz{Time: c.ExecuteAt, Valid: true}, AfterID: mustPgUUID(after), PageLimit: int32(o.Limit + 1)})
	if err != nil {
		return out, err
	}
	entries := make([]api.EnvironmentCacheSchedule, 0, len(rows))
	for _, raw := range rows {
		v, e := decodeCacheSchedule(raw)
		if e != nil {
			return out, e
		}
		if v.Scope != scope || v.ProjectID != current.ProjectID {
			return out, ErrRouteHealthRevision
		}
		entries = append(entries, v)
	}
	out = scheduleListPage(current, o, c, entries)
	return out, tx.Commit(ctx)
}
func (m *MemStore) ListEnvironmentCacheSchedules(_ context.Context, account, app, scope string, o api.EnvironmentCacheScheduleListOptions) (api.EnvironmentCacheScheduleList, error) {
	var out api.EnvironmentCacheScheduleList
	o, err := api.NormalizeEnvironmentCacheScheduleList(o)
	if err != nil {
		return out, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.environmentCachePolicyLocked(account, app, scope)
	if err != nil {
		return out, err
	}
	c, err := api.ParseEnvironmentCacheScheduleCursor(o, app, current.ProjectID, current.EnvironmentID)
	if err != nil {
		return out, ErrInvalidArgument
	}
	entries := []api.EnvironmentCacheSchedule{}
	for _, row := range m.environmentCacheSchedules {
		v := row.Value
		if row.Account != account || v.AppID != app || v.EnvironmentID != current.EnvironmentID || o.Status != "" && v.Status != o.Status || o.From != nil && v.ExecuteAt.Before(*o.From) || o.Until != nil && !v.ExecuteAt.Before(*o.Until) {
			continue
		}
		if o.After != "" && (v.ExecuteAt.Before(c.ExecuteAt) || v.ExecuteAt.Equal(c.ExecuteAt) && v.ScheduleID <= c.ScheduleID) {
			continue
		}
		entries = append(entries, cloneCacheSchedule(v))
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		return a.ExecuteAt.Before(b.ExecuteAt) || a.ExecuteAt.Equal(b.ExecuteAt) && a.ScheduleID < b.ScheduleID
	})
	return scheduleListPage(current, o, c, entries), nil
}
func rescheduleProposal(old api.EnvironmentCacheSchedule, id string, req api.RescheduleEnvironmentCacheRequest) (api.CreateEnvironmentCacheScheduleRequest, error) {
	if old.ScheduleID != id || old.AppID != req.AppID || old.ProjectID != req.ProjectID || old.EnvironmentID != req.EnvironmentID || !old.ExecuteAt.Equal(req.ExpectedExecuteAt) {
		return api.CreateEnvironmentCacheScheduleRequest{}, ErrConflict
	}
	return api.EnvironmentCacheScheduleReplacement(old, req.ReplacementID, req.ExecuteAt)
}
func replacementMatches(old, next api.EnvironmentCacheSchedule, proposed api.CreateEnvironmentCacheScheduleRequest) bool {
	return old.Status == "cancelled" && old.ReplacedByScheduleID == next.ScheduleID && next.ReplacesScheduleID == old.ScheduleID && old.Scope == next.Scope && reflect.DeepEqual(normalizeSchedule(next.CreateEnvironmentCacheScheduleRequest), proposed)
}
func (s *PgStore) RescheduleEnvironmentCache(ctx context.Context, account, app, scope, id string, req api.RescheduleEnvironmentCacheRequest) (api.EnvironmentCacheSchedule, error) {
	var out api.EnvironmentCacheSchedule
	if api.ValidateRescheduleEnvironmentCache(id, req) != nil || req.AppID != app {
		return out, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	owner, err := pgRouteMonitorOwner(ctx, tx, account, app, true)
	if err != nil {
		return out, err
	}
	if !owner.Account.MayDeploy() {
		return out, ErrEnvironmentCacheAccount
	}
	current, err := pgEnvironmentCachePolicy(ctx, tx, account, app, scope, true)
	if err != nil {
		return out, err
	}
	if current.ProjectID != req.ProjectID || current.EnvironmentID != req.EnvironmentID {
		return out, ErrRouteHealthRevision
	}
	q := sqlc.New()
	key := sqlc.LockEnvironmentCacheScheduleParams{ScheduleID: mustPgUUID(id), AccountID: mustPgUUID(account), AppID: mustPgUUID(app), EnvironmentID: mustPgUUID(current.EnvironmentID)}
	raw, err := q.LockEnvironmentCacheSchedule(ctx, tx, key)
	if err != nil {
		return out, routePolicyReadError(err)
	}
	old, err := decodeCacheSchedule(raw)
	if err != nil {
		return out, err
	}
	if old.Scope != scope {
		return out, ErrRouteHealthRevision
	}
	proposed, err := rescheduleProposal(old, id, req)
	if err != nil {
		return out, err
	}
	nextKey := sqlc.ReadEnvironmentCacheScheduleParams{ScheduleID: mustPgUUID(req.ReplacementID), AccountID: key.AccountID, AppID: key.AppID, EnvironmentID: key.EnvironmentID}
	raw, err = q.ReadEnvironmentCacheSchedule(ctx, tx, nextKey)
	if err == nil {
		out, err = decodeCacheSchedule(raw)
		if err != nil {
			return out, err
		}
		if !replacementMatches(old, out, proposed) {
			return out, ErrConflict
		}
		return out, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if old.Status != "scheduled" {
		return out, ErrEnvironmentCacheScheduleClaimed
	}
	// Replacement consumes the original active slot. No additional quota admission.
	if err = scheduleAdmission(proposed, current, 0, time.Now()); err != nil {
		return out, err
	}
	if proposed.Policy != nil {
		_, err = q.ReadEnvironmentCacheReceipt(ctx, tx, sqlc.ReadEnvironmentCacheReceiptParams{AccountID: key.AccountID, RequestID: nextKey.ScheduleID})
	} else {
		_, err = q.ReadEnvironmentCachePurge(ctx, tx, sqlc.ReadEnvironmentCachePurgeParams{AccountID: key.AccountID, RequestID: nextKey.ScheduleID})
	}
	if err == nil {
		return out, ErrConflict
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	n, err := q.ReplaceEnvironmentCacheSchedule(ctx, tx, sqlc.ReplaceEnvironmentCacheScheduleParams{ReplacementID: nextKey.ScheduleID, ScheduleID: key.ScheduleID})
	if err != nil {
		return out, err
	}
	if n != 1 {
		return out, ErrEnvironmentCacheScheduleClaimed
	}
	body, _ := json.Marshal(proposed)
	_, err = q.InsertEnvironmentCacheSchedule(ctx, tx, sqlc.InsertEnvironmentCacheScheduleParams{ScheduleID: nextKey.ScheduleID, AccountID: key.AccountID, AppID: key.AppID, ProjectID: mustPgUUID(req.ProjectID), EnvironmentID: key.EnvironmentID, Scope: scope, Request: body, ExecuteAt: pgtype.Timestamptz{Time: proposed.ExecuteAt, Valid: true}})
	if err != nil {
		return out, mapErr(err)
	}
	if err = q.LinkEnvironmentCacheScheduleReplacement(ctx, tx, sqlc.LinkEnvironmentCacheScheduleReplacementParams{OriginalID: key.ScheduleID, ScheduleID: nextKey.ScheduleID}); err != nil {
		return out, err
	}
	raw, err = q.ReadEnvironmentCacheSchedule(ctx, tx, nextKey)
	if err != nil {
		return out, err
	}
	out, err = decodeCacheSchedule(raw)
	if err != nil {
		return out, err
	}
	old.Status = "cancelled"
	old.ReplacedByScheduleID = out.ScheduleID
	if err = appendCacheScheduleEvent(ctx, tx, account, old, "rescheduled"); err != nil {
		return out, err
	}
	if err = appendCacheScheduleEvent(ctx, tx, account, out, "accepted"); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (m *MemStore) RescheduleEnvironmentCache(_ context.Context, account, app, scope, id string, req api.RescheduleEnvironmentCacheRequest) (api.EnvironmentCacheSchedule, error) {
	var out api.EnvironmentCacheSchedule
	if api.ValidateRescheduleEnvironmentCache(id, req) != nil || req.AppID != app {
		return out, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.environmentCachePolicyLocked(account, app, scope)
	if err != nil {
		return out, err
	}
	if !m.accounts[account].MayDeploy() {
		return out, ErrEnvironmentCacheAccount
	}
	if current.EnvironmentID != req.EnvironmentID || current.ProjectID != req.ProjectID {
		return out, ErrRouteHealthRevision
	}
	row, ok := m.environmentCacheSchedules[id]
	if !ok || row.Account != account || row.Value.AppID != app || row.Value.EnvironmentID != current.EnvironmentID {
		return out, ErrNotFound
	}
	old := row.Value
	if old.Scope != scope {
		return out, ErrRouteHealthRevision
	}
	proposed, err := rescheduleProposal(old, id, req)
	if err != nil {
		return out, err
	}
	if next, ok := m.environmentCacheSchedules[req.ReplacementID]; ok {
		if next.Account != account || !replacementMatches(old, next.Value, proposed) {
			return out, ErrConflict
		}
		return cloneCacheSchedule(next.Value), nil
	}
	if old.Status != "scheduled" {
		return out, ErrEnvironmentCacheScheduleClaimed
	}
	if err = scheduleAdmission(proposed, current, 0, time.Now()); err != nil {
		return out, err
	}
	if _, ok := m.environmentCacheReceipts[account+"\x00"+req.ReplacementID]; ok {
		return out, ErrConflict
	}
	if _, ok := m.environmentCachePurges[account+"/"+req.ReplacementID]; ok {
		return out, ErrConflict
	}
	now := time.Now().UTC()
	old.Status = "cancelled"
	old.FinishedAt = &now
	old.ReplacedByScheduleID = req.ReplacementID
	row.Value = old
	m.environmentCacheSchedules[id] = row
	out = api.EnvironmentCacheSchedule{CreateEnvironmentCacheScheduleRequest: proposed, Scope: scope, Status: "scheduled", CreatedAt: now, ReplacesScheduleID: id}
	m.environmentCacheSchedules[req.ReplacementID] = memoryCacheSchedule{Account: account, Value: out}
	m.appendCacheScheduleEventLocked(account, old, "rescheduled")
	m.appendCacheScheduleEventLocked(account, out, "accepted")
	return cloneCacheSchedule(out), nil
}
