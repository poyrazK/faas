package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var ErrEnvironmentCacheScheduleClaimed = fmt.Errorf("schedule is no longer pending: %w", ErrConflict)

// Schedule acceptance is durable delegation, independent of the creating MCP session.
type EnvironmentCacheScheduleStore interface {
	CreateEnvironmentCacheSchedule(context.Context, string, string, string, api.CreateEnvironmentCacheScheduleRequest) (api.EnvironmentCacheSchedule, error)
	GetEnvironmentCacheSchedule(context.Context, string, string, string, string) (api.EnvironmentCacheSchedule, error)
	CancelEnvironmentCacheSchedule(context.Context, string, string, string, string) (api.EnvironmentCacheSchedule, error)
}
type EnvironmentCacheScheduleClaim struct {
	AccountID  string
	LeaseToken string
	Schedule   api.EnvironmentCacheSchedule
}
type EnvironmentCacheScheduleWorker interface {
	EnvironmentCachePolicyStore
	EnvironmentCachePurgeStore
	RecoverEnvironmentCacheScheduleAction(context.Context, EnvironmentCacheScheduleClaim) (api.EnvironmentCacheSchedule, bool, error)
	ClaimEnvironmentCacheSchedule(context.Context) (EnvironmentCacheScheduleClaim, error)
	FinishEnvironmentCacheSchedule(context.Context, EnvironmentCacheScheduleClaim, api.EnvironmentCacheSchedule) error
}

func normalizeSchedule(req api.CreateEnvironmentCacheScheduleRequest) api.CreateEnvironmentCacheScheduleRequest {
	// Canonicalize time and copy mutable action data before storing/replaying it.
	raw, _ := json.Marshal(req)
	var out api.CreateEnvironmentCacheScheduleRequest
	_ = json.Unmarshal(raw, &out)
	out.ExecuteAt = out.ExecuteAt.UTC().Truncate(time.Microsecond)
	return out
}
func decodeCacheSchedule(raw []byte) (api.EnvironmentCacheSchedule, error) {
	var out api.EnvironmentCacheSchedule
	err := json.Unmarshal(raw, &out)
	return out, err
}
func scheduleAdmission(req api.CreateEnvironmentCacheScheduleRequest, current api.EnvironmentCachePolicy, count int64, now time.Time) error {
	if !req.ExecuteAt.After(now) || req.ExecuteAt.After(now.Add(api.EnvironmentCacheScheduleMaxDelay)) || count >= api.EnvironmentCacheScheduleLimit {
		return ErrInvalidArgument
	}
	if req.Policy != nil && *req.Policy.ExpectedRevision != current.Revision {
		return ErrRouteHealthRevision
	}
	return nil
}
func (s *PgStore) CreateEnvironmentCacheSchedule(ctx context.Context, account, app, scope string, req api.CreateEnvironmentCacheScheduleRequest) (api.EnvironmentCacheSchedule, error) {
	var out api.EnvironmentCacheSchedule
	if api.ValidateEnvironmentCacheSchedule(req) != nil || req.AppID != app {
		return out, ErrInvalidArgument
	}
	req = normalizeSchedule(req)
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
	if current.EnvironmentID != req.EnvironmentID || current.ProjectID != req.ProjectID {
		return out, ErrRouteHealthRevision
	}
	q := sqlc.New()
	key := sqlc.ReadEnvironmentCacheScheduleParams{ScheduleID: mustPgUUID(req.ScheduleID), AccountID: mustPgUUID(account), AppID: mustPgUUID(app), EnvironmentID: mustPgUUID(req.EnvironmentID)}
	raw, err := q.ReadEnvironmentCacheSchedule(ctx, tx, key)
	if err == nil {
		out, err = decodeCacheSchedule(raw)
		if err != nil {
			return out, err
		}
		if !reflect.DeepEqual(normalizeSchedule(out.CreateEnvironmentCacheScheduleRequest), req) {
			return out, ErrConflict
		}
		return out, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	count, err := q.CountEnvironmentCacheSchedules(ctx, tx, sqlc.CountEnvironmentCacheSchedulesParams{AccountID: key.AccountID, AppID: key.AppID, EnvironmentID: key.EnvironmentID})
	if err != nil {
		return out, err
	}
	if err = scheduleAdmission(req, current, count, time.Now()); err != nil {
		return out, err
	}
	// An existing immediate receipt must never masquerade as a future action.
	if req.Policy != nil {
		_, err = q.ReadEnvironmentCacheReceipt(ctx, tx, sqlc.ReadEnvironmentCacheReceiptParams{AccountID: key.AccountID, RequestID: key.ScheduleID})
	} else {
		_, err = q.ReadEnvironmentCachePurge(ctx, tx, sqlc.ReadEnvironmentCachePurgeParams{AccountID: key.AccountID, RequestID: key.ScheduleID})
	}
	if err == nil {
		return out, ErrConflict
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	body, _ := json.Marshal(req)
	raw, err = q.InsertEnvironmentCacheSchedule(ctx, tx, sqlc.InsertEnvironmentCacheScheduleParams{ScheduleID: key.ScheduleID, AccountID: key.AccountID, AppID: key.AppID, ProjectID: mustPgUUID(req.ProjectID), EnvironmentID: key.EnvironmentID, Scope: scope, Request: body, ExecuteAt: pgtype.Timestamptz{Time: req.ExecuteAt, Valid: true}})
	if err != nil {
		return out, mapErr(err)
	}
	out, err = decodeCacheSchedule(raw)
	if err != nil {
		return out, err
	}
	if err = appendCacheScheduleEvent(ctx, tx, account, out, "accepted"); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) cacheSchedule(ctx context.Context, account, app, scope, id string, cancel bool) (api.EnvironmentCacheSchedule, error) {
	var out api.EnvironmentCacheSchedule
	if _, err := uuid.Parse(id); err != nil {
		return out, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = pgRouteMonitorOwner(ctx, tx, account, app, cancel)
	if err != nil {
		return out, err
	}
	target, err := pgEnvironmentCachePolicy(ctx, tx, account, app, scope, cancel)
	if err != nil {
		return out, err
	}
	q := sqlc.New()
	key := sqlc.ReadEnvironmentCacheScheduleParams{ScheduleID: mustPgUUID(id), AccountID: mustPgUUID(account), AppID: mustPgUUID(app), EnvironmentID: mustPgUUID(target.EnvironmentID)}
	changed := int64(0)
	if cancel {
		changed, err = q.CancelEnvironmentCacheSchedule(ctx, tx, sqlc.CancelEnvironmentCacheScheduleParams{ScheduleID: key.ScheduleID, AccountID: key.AccountID, AppID: key.AppID, EnvironmentID: key.EnvironmentID})
		if err != nil {
			return out, err
		}
	}
	raw, err := q.ReadEnvironmentCacheSchedule(ctx, tx, key)
	if err != nil {
		return out, routePolicyReadError(err)
	}
	out, err = decodeCacheSchedule(raw)
	if err != nil {
		return out, err
	}
	if cancel && out.Status != "cancelled" {
		return out, ErrEnvironmentCacheScheduleClaimed
	}
	if out.Scope != scope || out.ProjectID != target.ProjectID {
		return out, ErrRouteHealthRevision
	}
	if changed == 1 {
		if err = appendCacheScheduleEvent(ctx, tx, account, out, "cancelled"); err != nil {
			return out, err
		}
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) GetEnvironmentCacheSchedule(ctx context.Context, account, app, scope, id string) (api.EnvironmentCacheSchedule, error) {
	return s.cacheSchedule(ctx, account, app, scope, id, false)
}
func (s *PgStore) CancelEnvironmentCacheSchedule(ctx context.Context, account, app, scope, id string) (api.EnvironmentCacheSchedule, error) {
	return s.cacheSchedule(ctx, account, app, scope, id, true)
}
func (s *PgStore) ClaimEnvironmentCacheSchedule(ctx context.Context) (EnvironmentCacheScheduleClaim, error) {
	out := EnvironmentCacheScheduleClaim{LeaseToken: uuid.NewString()}
	row, err := sqlc.New().ClaimEnvironmentCacheSchedule(ctx, s.pool, mustPgUUID(out.LeaseToken))
	if err != nil {
		return out, routePolicyReadError(err)
	}
	out.AccountID = row.AccountID
	out.Schedule, err = decodeCacheSchedule(row.Snapshot)
	return out, err
}
func (s *PgStore) FinishEnvironmentCacheSchedule(ctx context.Context, c EnvironmentCacheScheduleClaim, out api.EnvironmentCacheSchedule) error {
	if out.Status != "executed" && out.Status != "failed" {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	// All intent/event writers lock account, app and environment before the schedule
	// and sequence counter. Retain failed outcomes for soft-deleted app targets.
	if _, err = q.LockCacheScheduleCompletionAccount(ctx, tx, mustPgUUID(c.AccountID)); err != nil {
		return routePolicyReadError(err)
	}
	if _, err = q.LockCacheScheduleCompletionApp(ctx, tx, sqlc.LockCacheScheduleCompletionAppParams{AccountID: mustPgUUID(c.AccountID), AppID: mustPgUUID(c.Schedule.AppID)}); err != nil {
		return routePolicyReadError(err)
	}
	if _, err = q.LockCacheScheduleCompletionEnvironment(ctx, tx, sqlc.LockCacheScheduleCompletionEnvironmentParams{AccountID: mustPgUUID(c.AccountID), ProjectID: mustPgUUID(c.Schedule.ProjectID), EnvironmentID: mustPgUUID(c.Schedule.EnvironmentID)}); err != nil {
		return routePolicyReadError(err)
	}
	raw, _ := json.Marshal(struct {
		Policy *api.EnvironmentCachePolicy       `json:"policy_result,omitempty"`
		Purge  *api.EnvironmentCachePurgeReceipt `json:"purge_result,omitempty"`
	}{out.PolicyResult, out.PurgeResult})
	n, err := q.FinishEnvironmentCacheSchedule(ctx, tx, sqlc.FinishEnvironmentCacheScheduleParams{ScheduleID: mustPgUUID(c.Schedule.ScheduleID), LeaseToken: mustPgUUID(c.LeaseToken), Status: out.Status, ErrorCode: out.ErrorCode, Result: raw})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	raw, err = q.ReadEnvironmentCacheSchedule(ctx, tx, sqlc.ReadEnvironmentCacheScheduleParams{AccountID: mustPgUUID(c.AccountID), AppID: mustPgUUID(c.Schedule.AppID), EnvironmentID: mustPgUUID(c.Schedule.EnvironmentID), ScheduleID: mustPgUUID(c.Schedule.ScheduleID)})
	if err != nil {
		return err
	}
	saved, err := decodeCacheSchedule(raw)
	if err != nil {
		return err
	}
	if err = appendCacheScheduleEvent(ctx, tx, c.AccountID, saved, saved.Status); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Recover receipts before eligibility/revision checks: a previous attempt may have
// committed before its reply was lost, even if the account or policy changed later.
func (s *PgStore) RecoverEnvironmentCacheScheduleAction(ctx context.Context, c EnvironmentCacheScheduleClaim) (api.EnvironmentCacheSchedule, bool, error) {
	out := c.Schedule
	if out.Policy != nil {
		row, err := sqlc.New().ReadEnvironmentCacheReceipt(ctx, s.pool, sqlc.ReadEnvironmentCacheReceiptParams{AccountID: mustPgUUID(c.AccountID), RequestID: mustPgUUID(out.ScheduleID)})
		if errors.Is(err, pgx.ErrNoRows) {
			return out, false, nil
		}
		if err != nil {
			return out, false, err
		}
		if row.RequestHash != environmentCacheHash(out.Scope, *out.Policy) {
			return out, false, ErrConflict
		}
		var policy api.EnvironmentCachePolicy
		if err = json.Unmarshal(row.Response, &policy); err != nil {
			return out, false, err
		}
		out.PolicyResult = &policy
	} else if out.Purge != nil {
		raw, err := sqlc.New().ReadEnvironmentCachePurge(ctx, s.pool, sqlc.ReadEnvironmentCachePurgeParams{AccountID: mustPgUUID(c.AccountID), RequestID: mustPgUUID(out.ScheduleID)})
		if errors.Is(err, pgx.ErrNoRows) {
			return out, false, nil
		}
		if err != nil {
			return out, false, err
		}
		receipt, err := decodeEnvironmentPurge(raw, out.Scope)
		if err != nil {
			return out, false, err
		}
		if !purgeMatches(receipt, *out.Purge) {
			return out, false, ErrConflict
		}
		out.PurgeResult = &receipt
	} else {
		return out, false, ErrInvalidArgument
	}
	return out, true, nil
}
