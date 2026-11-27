package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtask"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func taskVersionFromSQL(r sqlc.ExecutionTaskVersion) (ExecutionTaskVersion, error) {
	var meta api.ExecutionTaskVersionResponse
	if err := json.Unmarshal(r.Metadata, &meta); err != nil {
		return ExecutionTaskVersion{}, err
	}
	return ExecutionTaskVersion{ID: pgUUIDString(r.ID), AccountID: pgUUIDString(r.AccountID), Name: r.Name, Version: int(r.Version), Digest: r.Digest, EnvironmentID: pgUUIDStringOrEmpty(r.EnvironmentID), Runtime: api.ExecutionRuntime(r.Runtime), Metadata: meta, Status: r.Status, SealedDefinition: append([]byte(nil), r.SealedDefinition...), PayloadKID: r.Kid, CreatedAt: r.CreatedAt.Time}, nil
}
func (s *PgStore) PublishExecutionTaskVersion(ctx context.Context, v ExecutionTaskVersion) (ExecutionTaskVersion, bool, error) {
	if err := validateExecutionTaskVersion(v); err != nil {
		return ExecutionTaskVersion{}, false, err
	}
	accountID, err := parsePgUUID(v.AccountID)
	if err != nil {
		return ExecutionTaskVersion{}, false, ErrExecutionInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ExecutionTaskVersion{}, false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	account, err := q.ExecutionLockAccount(ctx, tx, accountID)
	if err != nil {
		return ExecutionTaskVersion{}, false, mapErr(err)
	}
	old, err := q.RunTaskVersionGet(ctx, tx, sqlc.RunTaskVersionGetParams{AccountID: accountID, Name: v.Name, Version: int32(v.Version)})
	if err == nil {
		if old.Digest != v.Digest {
			return ExecutionTaskVersion{}, false, ErrExecutionTaskConflict
		}
		existing, e := taskVersionFromSQL(old)
		return existing, true, e
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ExecutionTaskVersion{}, false, err
	}
	stats, err := q.RunTaskVersionStats(ctx, tx, accountID)
	if err != nil {
		return ExecutionTaskVersion{}, false, err
	}
	exists, err := q.RunTaskNameExists(ctx, tx, sqlc.RunTaskNameExistsParams{AccountID: accountID, Name: v.Name})
	if err != nil {
		return ExecutionTaskVersion{}, false, err
	}
	if err := taskQuota(api.Plan(account.Plan), stats.Versions, stats.Names, stats.SealedBytes, !exists, int64(len(v.SealedDefinition))); err != nil {
		return ExecutionTaskVersion{}, false, err
	}
	endpoints := make([]pgtype.UUID, 0, len(v.Metadata.AgentEndpointIDs))
	for _, id := range v.Metadata.AgentEndpointIDs {
		endpoints = append(endpoints, mustPgUUID(id))
	}
	owned, err := q.RunTaskEndpointsOwned(ctx, tx, sqlc.RunTaskEndpointsOwnedParams{AccountID: accountID, EndpointIds: endpoints})
	if err != nil {
		return ExecutionTaskVersion{}, false, err
	}
	if !owned {
		return ExecutionTaskVersion{}, false, ErrNotFound
	}
	var environment pgtype.UUID
	if v.EnvironmentID != "" {
		environment = mustPgUUID(v.EnvironmentID)
	}
	raw, err := json.Marshal(v.Metadata)
	if err != nil {
		return ExecutionTaskVersion{}, false, err
	}
	row, err := q.RunTaskVersionInsert(ctx, tx, sqlc.RunTaskVersionInsertParams{ID: mustPgUUID(v.ID), AccountID: accountID, Name: v.Name, Version: int32(v.Version), Digest: v.Digest, Metadata: raw, EnvironmentID: environment, Runtime: string(v.Runtime), AgentEndpointIds: endpoints, SealedDefinition: v.SealedDefinition, Kid: v.PayloadKID})
	if err != nil {
		return ExecutionTaskVersion{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ExecutionTaskVersion{}, false, err
	}
	out, err := taskVersionFromSQL(row)
	return out, false, err
}
func (s *PgStore) ExecutionTaskVersionByName(ctx context.Context, account, name string, version int) (ExecutionTaskVersion, error) {
	return readTaskVersion(ctx, s.pool, account, name, version)
}
func (r executionAdmissionTxReader) ExecutionTaskVersionByName(ctx context.Context, account, name string, version int) (ExecutionTaskVersion, error) {
	return readTaskVersion(ctx, r.tx, account, name, version)
}
func readTaskVersion(ctx context.Context, db sqlc.DBTX, account, name string, version int) (ExecutionTaskVersion, error) {
	if !validEnvironmentID(account) || !runtask.ValidRef(name, version) {
		return ExecutionTaskVersion{}, ErrNotFound
	}
	row, err := sqlc.New().RunTaskVersionGet(ctx, db, sqlc.RunTaskVersionGetParams{AccountID: mustPgUUID(account), Name: name, Version: int32(version)})
	if err != nil {
		return ExecutionTaskVersion{}, mapErr(err)
	}
	return taskVersionFromSQL(row)
}
func (s *PgStore) ListExecutionTaskVersions(ctx context.Context, account, afterName string, afterVersion, limit int) ([]ExecutionTaskVersion, error) {
	if !validEnvironmentID(account) {
		return nil, ErrNotFound
	}
	if limit < 1 || limit > api.ExecutionTaskListMax+1 {
		limit = api.ExecutionTaskListMax + 1
	}
	rows, err := sqlc.New().RunTaskVersionList(ctx, s.pool, sqlc.RunTaskVersionListParams{AccountID: mustPgUUID(account), AfterName: afterName, AfterVersion: int32(afterVersion), PageLimit: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]ExecutionTaskVersion, 0, len(rows))
	for _, r := range rows {
		v, err := taskVersionFromSQL(sqlc.ExecutionTaskVersion(r))
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *PgStore) RetireExecutionTaskVersion(ctx context.Context, account, name string, version int) error {
	if !validEnvironmentID(account) || !runtask.ValidRef(name, version) {
		return ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	if _, err = q.ExecutionLockAccount(ctx, tx, mustPgUUID(account)); err != nil {
		return mapErr(err)
	}
	n, err := q.RunTaskVersionRetire(ctx, tx, sqlc.RunTaskVersionRetireParams{AccountID: mustPgUUID(account), Name: name, Version: int32(version)})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}
func runTaskInvocationFromSQL(r sqlc.RunTaskInvocationGetRow) (ExecutionTaskInvocation, error) {
	v := ExecutionTaskInvocation{ExecutionID: pgUUIDString(r.ExecutionID), AccountID: pgUUIDString(r.AccountID), VersionID: pgUUIDString(r.VersionID), Ref: api.ExecutionTaskRef{ID: pgUUIDString(r.VersionID), Name: r.Name, Version: int(r.Version), Digest: r.Digest}, RequestDigest: append([]byte(nil), r.RequestDigest...)}
	if r.CallbackKey != nil {
		var c ExecutionTaskCallback
		if err := json.Unmarshal(r.Callback, &c); err != nil {
			return v, err
		}
		v.Callback = &c
	}
	if r.DeliveredAt.Valid {
		t := r.DeliveredAt.Time
		v.DeliveredAt = &t
	}
	return v, nil
}
func readTaskInvocation(ctx context.Context, db sqlc.DBTX, account, id string) (ExecutionTaskInvocation, error) {
	if !validEnvironmentID(account) || !validEnvironmentID(id) {
		return ExecutionTaskInvocation{}, ErrNotFound
	}
	row, err := sqlc.New().RunTaskInvocationGet(ctx, db, sqlc.RunTaskInvocationGetParams{AccountID: mustPgUUID(account), ExecutionID: mustPgUUID(id)})
	if err != nil {
		return ExecutionTaskInvocation{}, mapErr(err)
	}
	return runTaskInvocationFromSQL(row)
}
func (s *PgStore) ExecutionTaskInvocationByID(ctx context.Context, account, id string) (ExecutionTaskInvocation, error) {
	return readTaskInvocation(ctx, s.pool, account, id)
}
func (s *PgStore) PendingExecutionTaskCallbacks(ctx context.Context, afterID string, limit int) ([]ExecutionTaskInvocation, error) {
	if limit < 1 || limit > api.ExecutionTaskCallbackBatch {
		limit = api.ExecutionTaskCallbackBatch
	}
	if afterID != "" && !validEnvironmentID(afterID) {
		return nil, ErrExecutionInvalid
	}
	rows, err := sqlc.New().RunTaskCallbacksPending(ctx, s.pool, sqlc.RunTaskCallbacksPendingParams{AfterID: afterID, BatchLimit: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]ExecutionTaskInvocation, 0, len(rows))
	for _, r := range rows {
		v, err := runTaskInvocationFromSQL(sqlc.RunTaskInvocationGetRow(r))
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *PgStore) MarkExecutionTaskCallbackDelivered(ctx context.Context, account, id string) error {
	if !validEnvironmentID(account) || !validEnvironmentID(id) {
		return ErrNotFound
	}
	n, err := sqlc.New().RunTaskCallbackDelivered(ctx, s.pool, sqlc.RunTaskCallbackDeliveredParams{AccountID: mustPgUUID(account), ExecutionID: mustPgUUID(id)})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotFound
	}
	return nil
}
func validateTaskAdmissionTx(ctx context.Context, tx pgx.Tx, p CreateExecutionParams) (ExecutionTaskVersion, error) {
	if p.Task == nil {
		return ExecutionTaskVersion{}, nil
	}
	if !validEnvironmentID(p.Task.VersionID) || p.Task.RequestDigest == [32]byte{} || validateExecutionTaskCallback(p.Task.Callback) != nil {
		return ExecutionTaskVersion{}, ErrExecutionInvalid
	}
	row, err := sqlc.New().RunTaskVersionAdmission(ctx, tx, sqlc.RunTaskVersionAdmissionParams{AccountID: mustPgUUID(p.AccountID), ID: mustPgUUID(p.Task.VersionID)})
	if err != nil {
		return ExecutionTaskVersion{}, errors.Join(ErrExecutionTaskUnavailable, err)
	}
	v, err := taskVersionFromSQL(row)
	if err != nil {
		return v, err
	}
	if err := matchTaskAdmission(v, p); err != nil {
		return v, err
	}
	if endpoint := runCallbackEndpoint(p.Task); endpoint != "" {
		owned, err := sqlc.New().RunTaskEndpointsOwned(ctx, tx, sqlc.RunTaskEndpointsOwnedParams{AccountID: mustPgUUID(p.AccountID), EndpointIds: []pgtype.UUID{mustPgUUID(endpoint)}})
		if err != nil {
			return v, err
		}
		if !owned {
			if p.Task.PipelineEndpointID != "" {
				return v, ErrExecutionTaskUnavailable
			}
			return v, ErrNotFound
		}
	}
	return v, nil
}
func addTaskInvocationTx(ctx context.Context, tx pgx.Tx, p CreateExecutionParams, r *Execution, v ExecutionTaskVersion) error {
	if p.Task == nil {
		return nil
	}
	var key []byte
	if p.Task.Callback != nil {
		key = p.Task.Callback.Key()
	}
	if err := sqlc.New().RunTaskInvocationInsert(ctx, tx, sqlc.RunTaskInvocationInsertParams{ExecutionID: mustPgUUID(r.ID), AccountID: mustPgUUID(r.AccountID), VersionID: mustPgUUID(v.ID), RequestDigest: p.Task.RequestDigest[:], CallbackKey: key, Callback: taskCallbackJSON(p.Task.Callback)}); err != nil {
		return fmt.Errorf("create Run task invocation: %w", err)
	}
	ref := api.ExecutionTaskRef{ID: v.ID, Name: v.Name, Version: v.Version, Digest: v.Digest}
	r.Task = &ref
	return nil
}
func attachTaskRef(ctx context.Context, db sqlc.DBTX, r *Execution) error {
	if err := attachRunAttribution(ctx, db, r); err != nil {
		return err
	}
	inv, err := readTaskInvocation(ctx, db, r.AccountID, r.ID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	r.Task = &inv.Ref
	return nil
}

var _ ExecutionTaskStore = (*PgStore)(nil)

func (s *PgStore) attachTaskRefs(ctx context.Context, rows []Execution) ([]Execution, error) {
	if len(rows) == 0 {
		return rows, nil
	}
	ids := make([]pgtype.UUID, len(rows))
	byID := make(map[string]int, len(rows))
	for i, r := range rows {
		ids[i] = mustPgUUID(r.ID)
		byID[r.ID] = i
	}
	attributions, err := sqlc.New().RunAttributionsGet(ctx, s.pool, sqlc.RunAttributionsGetParams{AccountID: mustPgUUID(rows[0].AccountID), ExecutionIds: ids})
	if err != nil {
		return nil, err
	}
	for _, a := range attributions {
		if i, ok := byID[pgUUIDString(a.ExecutionID)]; ok && (a.CustomerRef != "" || a.AgentRef != "") {
			rows[i].Attribution = &api.RunAttribution{CustomerRef: a.CustomerRef, AgentRef: a.AgentRef}
		}
	}
	refs, err := sqlc.New().RunTaskInvocationRefs(ctx, s.pool, sqlc.RunTaskInvocationRefsParams{AccountID: mustPgUUID(rows[0].AccountID), ExecutionIds: ids})
	if err != nil {
		return nil, err
	}
	for _, ref := range refs {
		if i, ok := byID[pgUUIDString(ref.ExecutionID)]; ok {
			rows[i].Task = &api.ExecutionTaskRef{ID: pgUUIDString(ref.ID), Name: ref.Name, Version: int(ref.Version), Digest: ref.Digest}
		}
	}
	return rows, nil
}
