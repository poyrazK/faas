package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
)

func describeCheck(r sqlc.PipelineCheckGetRow) (ConnectionCheck, error) {
	var payload api.DataConnectionCheck
	if len(r.Result) > api.DataPipelineCheckMaxBytes || json.Unmarshal(r.Result, &payload) != nil {
		return ConnectionCheck{}, ErrUnavailable
	}
	payload.ID, payload.ConnectionID, payload.ConnectionRevision = uid(r.ID), uid(r.ConnectionID), r.ConnectionRevision
	payload.DatabaseID, payload.State, payload.CreatedAt = r.DatabaseID, r.State, stamp(r.CreatedAt)
	payload.ObservedAt, payload.ExpiresAt, payload.Fresh = stamp(r.ObservedAt), "", false
	if payload.Checks == nil {
		payload.Checks = []api.DataConnectionIssue{}
	}
	if payload.Tables == nil {
		payload.Tables = []api.DataSourceTable{}
	}
	if r.ConnectionRevision != r.CurrentRevision {
		payload.State = "stale"
	}
	if r.ObservedAt.Valid {
		expires := r.ObservedAt.Time.Add(api.DataPipelineCheckMaxAge)
		payload.ExpiresAt = expires.UTC().Format(time.RFC3339Nano)
		now := time.Now()
		payload.Fresh = (payload.State == "passed" || payload.State == "blocked") && !now.Before(r.ObservedAt.Time) && now.Before(expires)
	}
	expireCheckCapacity(&payload, time.Now())
	return ConnectionCheck{DataConnectionCheck: payload, AccountID: uid(r.AccountID), LeaseToken: r.LeaseToken}, nil
}

func checkRecord(r sqlc.DataConnectionCheck) sqlc.PipelineCheckGetRow {
	return sqlc.PipelineCheckGetRow{ConnectionID: r.ConnectionID, AccountID: r.AccountID, ID: r.ID,
		ConnectionRevision: r.ConnectionRevision, CurrentRevision: r.ConnectionRevision, DatabaseID: r.DatabaseID,
		State: r.State, Result: r.Result, LeaseToken: r.LeaseToken, LeaseUntil: r.LeaseUntil, CreatedAt: r.CreatedAt, ObservedAt: r.ObservedAt}
}

func (s *PostgresStore) Check(ctx context.Context, account, id string) (ConnectionCheck, error) {
	if !validID(account) || !validID(id) {
		return ConnectionCheck{}, ErrNotFound
	}
	r, err := s.q.PipelineCheckGet(ctx, s.pool, sqlc.PipelineCheckGetParams{AccountID: account, ConnectionID: id})
	if err != nil {
		return ConnectionCheck{}, storeError(err)
	}
	return describeCheck(r)
}

func (s *PostgresStore) RequestCheck(ctx context.Context, account, id string, revision int64, database string) (ConnectionCheck, error) {
	if !validID(account) || !validID(id) {
		return ConnectionCheck{}, ErrNotFound
	}
	if revision < 1 || (database != "" && !validID(database)) {
		return ConnectionCheck{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ConnectionCheck{}, storeError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = s.q.PipelineAccountLock(ctx, tx, account); err != nil {
		return ConnectionCheck{}, storeError(err)
	}
	c, err := s.q.PipelineCheckConnectionLock(ctx, tx, sqlc.PipelineCheckConnectionLockParams{AccountID: account, ID: id})
	if err != nil {
		return ConnectionCheck{}, storeError(err)
	}
	if c.Revision != revision {
		return ConnectionCheck{}, ErrConflict
	}
	old, err := s.q.PipelineCheckGet(ctx, tx, sqlc.PipelineCheckGetParams{AccountID: account, ConnectionID: id})
	if err == nil && old.ConnectionRevision == revision && old.DatabaseID == database {
		check, decodeErr := describeCheck(old)
		if decodeErr != nil {
			return ConnectionCheck{}, decodeErr
		}
		// Coalesce active and very recent requests. Retrying a lost HTTP response
		// must not continuously replace a worker's lease or flood the source.
		age := time.Since(old.CreatedAt.Time)
		if old.State == "pending" || old.State == "running" || (age >= 0 && age < api.DataPipelineCheckRetryInterval) {
			return check, nil
		}
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ConnectionCheck{}, storeError(err)
	}
	r, err := s.q.PipelineCheckRequest(ctx, tx, sqlc.PipelineCheckRequestParams{AccountID: account, ConnectionID: id, ID: uuid.NewString(), ConnectionRevision: revision, DatabaseID: database})
	if err != nil {
		return ConnectionCheck{}, storeError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return ConnectionCheck{}, storeError(err)
	}
	return describeCheck(checkRecord(r))
}

func (s *PostgresStore) ClaimCheck(ctx context.Context, token string) (ConnectionCheck, error) {
	r, err := s.q.PipelineCheckClaim(ctx, s.pool, token)
	if err != nil {
		return ConnectionCheck{}, storeError(err)
	}
	return describeCheck(checkRecord(r))
}

func (s *PostgresStore) FinishCheck(ctx context.Context, c ConnectionCheck, result api.DataConnectionCheck) error {
	if result.State != "passed" && result.State != "blocked" {
		return ErrInvalid
	}
	payload := api.DataConnectionCheck{Checks: result.Checks, Tables: result.Tables, Truncated: result.Truncated, Capacity: result.Capacity}
	blob, err := json.Marshal(payload)
	if err != nil || len(blob) > api.DataPipelineCheckMaxBytes {
		return ErrInvalid
	}
	bounded, err := json.MarshalIndent(payload, "", " ")
	if err != nil || len(bounded) > api.DataPipelineCheckMaxBytes {
		return ErrInvalid
	}
	n, err := s.q.PipelineCheckFinish(ctx, s.pool, sqlc.PipelineCheckFinishParams{AccountID: c.AccountID, ID: c.ID,
		ConnectionRevision: c.ConnectionRevision, LeaseToken: c.LeaseToken, State: result.State, Result: blob})
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Service) RequestCheck(ctx context.Context, account, id string, r api.RequestDataConnectionCheck) (api.DataConnectionCheck, error) {
	if err := s.admit(ctx, account); err != nil {
		return api.DataConnectionCheck{}, err
	}
	if r.DatabaseID != "" {
		if !validID(r.DatabaseID) {
			return api.DataConnectionCheck{}, ErrInvalid
		}
		if s.ValidateTarget == nil {
			return api.DataConnectionCheck{}, ErrUnavailable
		}
		if err := s.ValidateTarget(ctx, account, r.DatabaseID); err != nil {
			return api.DataConnectionCheck{}, err
		}
	}
	check, err := s.Store.RequestCheck(ctx, account, id, r.ExpectedRevision, r.DatabaseID)
	return check.DataConnectionCheck, err
}

func checkSelection(c api.DataConnectionCheck, tables []string) error {
	if !c.Fresh || c.State != "passed" {
		return ErrCheckRequired
	}
	for _, name := range tables {
		found := false
		for _, table := range c.Tables {
			if table.Name == name && table.Supported {
				found = true
				break
			}
		}
		if !found {
			return ErrCheckRequired
		}
	}
	return nil
}
