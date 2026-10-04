package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
)

type exclusivePostgresTx struct {
	ctx  context.Context
	db   pgx.Tx
	q    *sqlc.Queries
	plan api.Plan
}

func (s *PgStore) exclusiveAtomic(ctx context.Context, run func(exclusiveTransaction) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = run(&exclusivePostgresTx{ctx: ctx, db: tx, q: sqlc.New()}); err != nil {
		return exclusiveSQLError(err)
	}
	return tx.Commit(ctx)
}
func exclusiveSQLError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
func exclusiveUUID(v pgtype.UUID) string {
	if !v.Valid {
		return ""
	}
	return uuid.UUID(v.Bytes).String()
}
func exclusiveTime(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	t := v.Time
	return &t
}
func exclusivePGTime(v *time.Time) pgtype.Timestamptz {
	if v == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *v, Valid: true}
}
func exclusivePGText(v string) pgtype.Text {
	if v == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: v, Valid: true}
}
func exclusivePGPolicy(p sqlc.ExclusiveWorkPolicy) (out ExclusiveWorkPolicy, err error) {
	out = ExclusiveWorkPolicy{ID: exclusiveUUID(p.ID), AccountID: exclusiveUUID(p.AccountID), Revision: p.Revision,
		Retired: p.Retired, CreatedAt: p.CreatedAt.Time, UpdatedAt: p.UpdatedAt.Time}
	err = json.Unmarshal(p.Configuration, &out.Policy)
	return
}
func exclusivePGKey(k sqlc.ExclusiveWorkKey) exclusiveKey {
	return exclusiveKey{ID: exclusiveUUID(k.ID), AccountID: exclusiveUUID(k.AccountID), PolicyID: exclusiveUUID(k.PolicyID), ScopeID: exclusiveUUID(k.ScopeID), EnvironmentID: k.EnvironmentID, Digest: k.KeyDigest, Generation: k.Generation, NextSequence: k.NextSequence}
}
func exclusivePGOperation(o sqlc.ExclusiveWorkOperation) (out ExclusiveOperation, err error) {
	out = ExclusiveOperation{ID: exclusiveUUID(o.ID), AccountID: exclusiveUUID(o.AccountID), KeyID: exclusiveUUID(o.KeyID), AppID: exclusiveUUID(o.AppID), JobID: exclusiveUUID(o.JobID),
		PlatformTenantID: exclusiveUUID(o.PlatformTenantID), Sequence: o.Sequence, State: o.State, PolicyRevision: o.PolicyRevision,
		Request: o.Request, RequestDigest: o.RequestDigest, EquivalenceDigest: o.EquivalenceDigest, IdempotencyDigest: o.IdempotencyDigest,
		Generation: o.Generation, ClaimToken: exclusiveUUID(o.ClaimToken), IncarnationID: o.IncarnationID,
		LeaseExpiresAt: exclusiveTime(o.LeaseExpiresAt), AttemptDeadline: exclusiveTime(o.AttemptDeadline),
		Result: o.Result, LastError: o.LastError, CreatedAt: o.CreatedAt.Time, CompletedAt: exclusiveTime(o.CompletedAt)}
	out.DueAt, out.Attempts, out.QuotaReserved = o.DueAt.Time, int(o.Attempts), o.QuotaReserved
	err = json.Unmarshal(o.Configuration, &out.Policy)
	return
}
func (tx *exclusivePostgresTx) lockAccount(account string) error {
	row, err := tx.q.LockExclusiveWorkAccount(tx.ctx, tx.db, account)
	tx.plan = api.Plan(row.Plan)
	return exclusiveSQLError(err)
}
func (tx *exclusivePostgresTx) appScope(account, id string) (string, error) {
	row, err := tx.q.ExclusiveWorkAppScope(tx.ctx, tx.db, sqlc.ExclusiveWorkAppScopeParams{AccountID: account, AppID: id})
	return row.ProjectID, exclusiveSQLError(err)
}
func (tx *exclusivePostgresTx) jobScope(account, id string) error {
	var found string
	err := tx.db.QueryRow(tx.ctx, `SELECT id::text FROM jobs WHERE id=$1::uuid AND account_id=$2::uuid AND status<>'deleted' FOR SHARE`, id, account).Scan(&found)
	return exclusiveSQLError(err)
}
func (tx *exclusivePostgresTx) jobRunScope(account, operationID string, generation int64) error {
	var found string
	err := tx.db.QueryRow(tx.ctx, `SELECT id::text FROM job_runs
		WHERE account_id=$1::uuid AND exclusive_operation_id=$2::uuid
		AND exclusive_generation=$3`, account, operationID, generation).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return exclusivework.ErrStaleOwner
	}
	return err
}
func (tx *exclusivePostgresTx) appTaskScope(account, operationID string, generation int64) error {
	var found string
	err := tx.db.QueryRow(tx.ctx, `SELECT id::text FROM app_tasks
		WHERE account_id=$1::uuid AND exclusive_operation_id=$2::uuid
		AND exclusive_generation=$3`, account, operationID, generation).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return exclusivework.ErrStaleOwner
	}
	return err
}
func (tx *exclusivePostgresTx) tenantScope(account, id string) error {
	row, err := tx.q.ExclusiveWorkTenantScope(tx.ctx, tx.db, sqlc.ExclusiveWorkTenantScopeParams{AccountID: account, TenantID: id})
	if err != nil {
		return exclusiveSQLError(err)
	}
	if row.Status != PlatformTenantActive {
		return ErrPlatformTenantSuspended
	}
	return nil
}
func (tx *exclusivePostgresTx) environmentScope(account, project, id string) error {
	if project == "" {
		return ErrNotFound
	}
	_, err := tx.q.ExclusiveWorkEnvironmentScope(tx.ctx, tx.db, sqlc.ExclusiveWorkEnvironmentScopeParams{AccountID: account, ProjectID: project, EnvironmentID: id})
	return exclusiveSQLError(err)
}
func (tx *exclusivePostgresTx) now() (time.Time, error) {
	v, err := tx.q.ExclusiveWorkClock(tx.ctx, tx.db)
	return v.Time, err
}
func (tx *exclusivePostgresTx) policy(account, name string) (ExclusiveWorkPolicy, error) {
	p, err := tx.q.ReadExclusiveWorkPolicy(tx.ctx, tx.db, sqlc.ReadExclusiveWorkPolicyParams{AccountID: account, Name: name})
	if err != nil {
		return ExclusiveWorkPolicy{}, exclusiveSQLError(err)
	}
	return exclusivePGPolicy(p)
}
func (tx *exclusivePostgresTx) policies(account string) (out []ExclusiveWorkPolicy, err error) {
	rows, err := tx.q.ListExclusiveWorkPolicies(tx.ctx, tx.db, account)
	if err != nil {
		return nil, err
	}
	out = []ExclusiveWorkPolicy{}
	for _, row := range rows {
		p, e := exclusivePGPolicy(row)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return
}
func (tx *exclusivePostgresTx) savePolicy(p ExclusiveWorkPolicy) (ExclusiveWorkPolicy, error) {
	config, err := json.Marshal(p.Policy)
	if err != nil {
		return ExclusiveWorkPolicy{}, err
	}
	incompatible, err := tx.q.CommitPolicyWouldInvalidateSource(tx.ctx, tx.db, sqlc.CommitPolicyWouldInvalidateSourceParams{
		AccountID: p.AccountID, Name: p.Policy.Name, Configuration: config, Retired: p.Retired,
	})
	if err != nil {
		return ExclusiveWorkPolicy{}, err
	}
	if incompatible {
		return ExclusiveWorkPolicy{}, ErrExclusivePolicyInUse
	}
	row, err := tx.q.SaveExclusiveWorkPolicy(tx.ctx, tx.db, sqlc.SaveExclusiveWorkPolicyParams{ID: p.ID, AccountID: p.AccountID, Name: p.Policy.Name, Configuration: config, Retired: p.Retired})
	if err != nil {
		return ExclusiveWorkPolicy{}, err
	}
	return exclusivePGPolicy(row)
}

func (tx *exclusivePostgresTx) policyInUse(policy ExclusiveWorkPolicy) (bool, error) {
	result, err := tx.q.ExclusiveWorkPolicyInUse(tx.ctx, tx.db, policy.ID)
	if err != nil {
		return false, err
	}
	if !result.Valid {
		return false, errors.New("exclusive policy usage query returned null")
	}
	return result.Bool, nil
}
func (tx *exclusivePostgresTx) ensureKey(k exclusiveKey) (exclusiveKey, error) {
	row, err := tx.q.EnsureExclusiveWorkKey(tx.ctx, tx.db, sqlc.EnsureExclusiveWorkKeyParams{ID: k.ID, AccountID: k.AccountID, PolicyID: k.PolicyID, ScopeID: k.ScopeID, EnvironmentID: k.EnvironmentID, KeyDigest: k.Digest})
	return exclusivePGKey(row), err
}
func (tx *exclusivePostgresTx) key(account, id string) (exclusiveKey, error) {
	row, err := tx.q.ReadExclusiveWorkKey(tx.ctx, tx.db, sqlc.ReadExclusiveWorkKeyParams{AccountID: account, ID: id})
	return exclusivePGKey(row), exclusiveSQLError(err)
}
func (tx *exclusivePostgresTx) saveKey(k exclusiveKey) error {
	return tx.q.SaveExclusiveWorkKey(tx.ctx, tx.db, sqlc.SaveExclusiveWorkKeyParams{ID: k.ID, Generation: k.Generation, NextSequence: k.NextSequence})
}
func (tx *exclusivePostgresTx) operation(account, id string) (ExclusiveOperation, error) {
	row, err := tx.q.ReadExclusiveWorkOperation(tx.ctx, tx.db, sqlc.ReadExclusiveWorkOperationParams{AccountID: account, ID: id})
	if err != nil {
		return ExclusiveOperation{}, exclusiveSQLError(err)
	}
	return exclusivePGOperation(row)
}
func (tx *exclusivePostgresTx) replay(key string, digest []byte) (ExclusiveOperation, error) {
	row, err := tx.q.ReadExclusiveWorkReplay(tx.ctx, tx.db, sqlc.ReadExclusiveWorkReplayParams{KeyID: key, Digest: digest})
	if err != nil {
		return ExclusiveOperation{}, exclusiveSQLError(err)
	}
	return exclusivePGOperation(row)
}
func (tx *exclusivePostgresTx) active(key string) (out []ExclusiveOperation, err error) {
	rows, err := tx.q.ListExclusiveWorkActive(tx.ctx, tx.db, key)
	if err != nil {
		return nil, err
	}
	out = []ExclusiveOperation{}
	for _, row := range rows {
		o, e := exclusivePGOperation(row)
		if e != nil {
			return nil, e
		}
		out = append(out, o)
	}
	return
}
func (tx *exclusivePostgresTx) pendingCount(account string) (int64, error) {
	return tx.q.CountExclusiveWorkPending(tx.ctx, tx.db, account)
}
func (tx *exclusivePostgresTx) insert(o ExclusiveOperation) (ExclusiveOperation, error) {
	config, err := json.Marshal(o.Policy)
	if err != nil {
		return ExclusiveOperation{}, err
	}
	row, err := tx.q.InsertExclusiveWorkOperation(tx.ctx, tx.db, sqlc.InsertExclusiveWorkOperationParams{ID: o.ID, AccountID: o.AccountID, KeyID: o.KeyID, AppID: exclusivePGText(o.AppID), JobID: exclusivePGText(o.JobID), TenantID: o.PlatformTenantID, Sequence: o.Sequence,
		PolicyRevision: o.PolicyRevision, Configuration: config, Request: o.Request, RequestDigest: o.RequestDigest, EquivalenceDigest: o.EquivalenceDigest, IdempotencyDigest: o.IdempotencyDigest})
	if err != nil {
		return ExclusiveOperation{}, err
	}
	return exclusivePGOperation(row)
}
func (tx *exclusivePostgresTx) save(o ExclusiveOperation) error {
	return tx.q.SaveExclusiveWorkOperation(tx.ctx, tx.db, sqlc.SaveExclusiveWorkOperationParams{ID: o.ID, State: o.State, Generation: o.Generation, ClaimToken: o.ClaimToken, IncarnationID: o.IncarnationID, LeaseExpiresAt: exclusivePGTime(o.LeaseExpiresAt), AttemptDeadline: exclusivePGTime(o.AttemptDeadline), Result: o.Result, LastError: o.LastError, CompletedAt: exclusivePGTime(o.CompletedAt), DueAt: pgtype.Timestamptz{Time: o.DueAt, Valid: true}, Attempts: int32(o.Attempts), QuotaReserved: o.QuotaReserved})
}

func (tx *exclusivePostgresTx) reserve(account string) error {
	cap := api.MustLimitsFor(tx.plan).MaxAsyncInvocationsPerAccount
	if err := tx.q.EnsureExclusiveWorkQuota(tx.ctx, tx.db, sqlc.EnsureExclusiveWorkQuotaParams{AccountID: account, MaxInflight: int32(cap)}); err != nil {
		return err
	}
	_, err := tx.q.ReserveExclusiveWorkQuota(tx.ctx, tx.db, account)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrQuotaExceeded
	}
	return err
}
func (tx *exclusivePostgresTx) effects(o ExclusiveOperation, effects []exclusivework.Effect) error {
	for _, e := range effects {
		if err := tx.q.InsertExclusiveWorkEffect(tx.ctx, tx.db, sqlc.InsertExclusiveWorkEffectParams{ID: uuid.NewString(), OperationID: o.ID, Generation: o.Generation, Name: e.Name, Payload: e.Payload}); err != nil {
			return err
		}
	}
	return nil
}

func (tx *exclusivePostgresTx) bindSubmission(key string, digest []byte, id string) error {
	if digest == nil {
		return nil
	}
	return tx.q.BindExclusiveWorkSubmission(tx.ctx, tx.db, sqlc.BindExclusiveWorkSubmissionParams{KeyID: key, Digest: digest, OperationID: id})
}

func (tx *exclusivePostgresTx) runtimeScope(account, app, id, wake, node string) error {
	_, err := tx.q.CheckExclusiveWorkRuntime(tx.ctx, tx.db, sqlc.CheckExclusiveWorkRuntimeParams{AccountID: account, AppID: app, InstanceID: id, WakeID: wake, NodeID: node})
	if errors.Is(err, pgx.ErrNoRows) {
		return exclusivework.ErrStaleOwner
	}
	return err
}
