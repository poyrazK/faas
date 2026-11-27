package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runenv"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func environmentFromSQL(row sqlc.ExecutionEnvironment) ExecutionEnvironment {
	var recipe api.CreateExecutionEnvironmentRequest
	_ = json.Unmarshal(row.Recipe, &recipe)
	return ExecutionEnvironment{ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID), PrincipalID: row.PrincipalID, Runtime: api.ExecutionRuntime(row.Runtime), Recipe: recipe, RecipeDigest: row.RecipeDigest, BaseRef: row.BaseRef, Status: row.Status, Lease: row.Lease, LeaseExpiresAt: row.LeaseExpiresAt.Time, BuildAttempts: int(row.BuildAttempts), ImageAttempts: int(row.ImageAttempts), ExportKey: row.ExportKey, ExportDigest: row.ExportDigest, BaseKey: row.BaseKey, ImageDigest: row.ImageDigest, GuestExecutorDigest: row.GuestExecutorDigest, ArtifactKeys: row.ArtifactKeys, ArtifactBytes: row.ArtifactBytes, FailureCode: row.FailureCode, CreatedAt: row.CreatedAt.Time}
}
func (s *PgStore) CreateExecutionEnvironment(ctx context.Context, row ExecutionEnvironment) (ExecutionEnvironment, error) {
	if err := validateExecutionEnvironment(row); err != nil {
		return ExecutionEnvironment{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ExecutionEnvironment{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	account := mustPgUUID(row.AccountID)
	acct, err := q.ExecutionLockAccount(ctx, tx, account)
	if err != nil {
		return ExecutionEnvironment{}, mapErr(err)
	}
	old, err := q.ExecutionEnvironmentExisting(ctx, tx, sqlc.ExecutionEnvironmentExistingParams{AccountID: account, PrincipalID: row.PrincipalID, RecipeDigest: row.RecipeDigest})
	if err == nil {
		return environmentFromSQL(old), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ExecutionEnvironment{}, err
	}
	count, err := q.ExecutionEnvironmentCount(ctx, tx, account)
	if err != nil {
		return ExecutionEnvironment{}, err
	}
	if count >= int64(api.ExecutionEnvironmentCountByPlan[api.Plan(acct.Plan).PlanIndex()]) {
		return ExecutionEnvironment{}, &ExecutionEnvironmentQuotaError{Limit: api.ExecutionEnvironmentCountByPlan[api.Plan(acct.Plan).PlanIndex()], Observed: int(count) + 1}
	}
	inserted, err := q.ExecutionEnvironmentInsert(ctx, tx, sqlc.ExecutionEnvironmentInsertParams{ID: mustPgUUID(row.ID), AccountID: account, PrincipalID: row.PrincipalID, Runtime: string(row.Runtime), Recipe: environmentRecipeJSON(row), RecipeDigest: row.RecipeDigest, BaseRef: row.BaseRef, InitialStatus: environmentWorkerStatus("queued", row.Recipe)})
	if err != nil {
		return ExecutionEnvironment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ExecutionEnvironment{}, err
	}
	return environmentFromSQL(inserted), nil
}
func (s *PgStore) ExecutionEnvironmentByID(ctx context.Context, account, id string) (ExecutionEnvironment, error) {
	if !runenv.ValidID(account) || !runenv.ValidID(id) {
		return ExecutionEnvironment{}, ErrNotFound
	}
	row, err := sqlc.New().ExecutionEnvironmentGet(ctx, s.pool, sqlc.ExecutionEnvironmentGetParams{AccountID: mustPgUUID(account), ID: mustPgUUID(id)})
	if err != nil {
		return ExecutionEnvironment{}, mapErr(err)
	}
	out := environmentFromSQL(row)
	diagnostics, err := sqlc.New().ExecutionEnvironmentReadDiagnostics(ctx, s.pool, sqlc.ExecutionEnvironmentReadDiagnosticsParams{ID: mustPgUUID(id), AccountID: mustPgUUID(account)})
	if err != nil {
		return ExecutionEnvironment{}, mapErr(err)
	}
	out.DiagnosticSequence = diagnostics.DiagnosticSequence
	if err := json.Unmarshal(diagnostics.Diagnostics, &out.Diagnostics); err != nil {
		return ExecutionEnvironment{}, err
	}
	return out, nil
}
func (s *PgStore) ListExecutionEnvironments(ctx context.Context, account, principal string, wide bool) ([]ExecutionEnvironment, error) {
	rows, err := sqlc.New().ExecutionEnvironmentList(ctx, s.pool, sqlc.ExecutionEnvironmentListParams{AccountID: mustPgUUID(account), Wide: wide, PrincipalID: principal})
	out := make([]ExecutionEnvironment, 0, len(rows))
	for _, row := range rows {
		out = append(out, environmentFromSQL(row))
	}
	return out, err
}
func (s *PgStore) RetireExecutionEnvironment(ctx context.Context, account, id, principal string, wide bool) error {
	if !runenv.ValidID(id) {
		return ErrNotFound
	}
	n, err := sqlc.New().ExecutionEnvironmentRetire(ctx, s.pool, sqlc.ExecutionEnvironmentRetireParams{AccountID: mustPgUUID(account), ID: mustPgUUID(id), Wide: wide, PrincipalID: principal})
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}
func (s *PgStore) ClaimExecutionEnvironment(ctx context.Context, phase string) (ExecutionEnvironment, error) {
	if phase != "build" && phase != "image" {
		return ExecutionEnvironment{}, ErrExecutionInvalid
	}
	q := sqlc.New()
	if err := q.ExecutionEnvironmentExpireClaims(ctx, s.pool); err != nil {
		return ExecutionEnvironment{}, err
	}
	queued, running := "queued", "building"
	if phase == "image" {
		queued, running = "built", "imaging"
	}
	row, err := q.ExecutionEnvironmentClaim(ctx, s.pool, sqlc.ExecutionEnvironmentClaimParams{QueuedStatus: queued, RunningStatus: running, SystemQueuedStatus: queued + "_system_v1", SystemRunningStatus: running + "_system_v1", Build: phase == "build", Lease: uuid.NewString(), LeaseSeconds: api.ExecutionEnvironmentLeaseSeconds})
	if err != nil {
		return ExecutionEnvironment{}, mapErr(err)
	}
	return environmentFromSQL(sqlc.ExecutionEnvironment(row)), nil
}
func environmentPublication(n int64, err error) error {
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrExecutionEnvironmentLease
	}
	return nil
}
func (s *PgStore) PublishExecutionEnvironmentBuild(ctx context.Context, c ExecutionEnvironment, digest string) error {
	if !validEnvironmentDigest(digest) {
		return ErrExecutionInvalid
	}
	n, err := sqlc.New().ExecutionEnvironmentPublishBuild(ctx, s.pool, sqlc.ExecutionEnvironmentPublishBuildParams{ID: mustPgUUID(c.ID), Lease: c.Lease, ExportDigest: digest})
	return environmentPublication(n, err)
}
func (s *PgStore) PublishExecutionEnvironmentImage(ctx context.Context, c ExecutionEnvironment, digest, executorDigest string, size int64) error {
	if !validEnvironmentDigest(digest) || !validEnvironmentDigest(executorDigest) || size <= 0 || size > api.ExecutionEnvironmentMaxArchiveBytes {
		return ErrExecutionInvalid
	}
	n, err := sqlc.New().ExecutionEnvironmentPublishImage(ctx, s.pool, sqlc.ExecutionEnvironmentPublishImageParams{ID: mustPgUUID(c.ID), Lease: c.Lease, ImageDigest: digest, GuestExecutorDigest: executorDigest, ArtifactBytes: size})
	return environmentPublication(n, err)
}
func (s *PgStore) FailExecutionEnvironment(ctx context.Context, c ExecutionEnvironment, code string) error {
	if !validEnvironmentFailure(code) {
		return ErrExecutionInvalid
	}
	n, err := sqlc.New().ExecutionEnvironmentFail(ctx, s.pool, sqlc.ExecutionEnvironmentFailParams{ID: mustPgUUID(c.ID), Lease: c.Lease, FailureCode: code})
	return environmentPublication(n, err)
}
func (s *PgStore) CollectableExecutionEnvironments(ctx context.Context) ([]ExecutionEnvironment, error) {
	rows, err := sqlc.New().ExecutionEnvironmentCollectable(ctx, s.pool, api.ExecutionEnvironmentGCBatch)
	out := make([]ExecutionEnvironment, 0, len(rows))
	for _, row := range rows {
		out = append(out, environmentFromSQL(row))
	}
	return out, err
}
func (s *PgStore) DeleteExecutionEnvironmentArtifacts(ctx context.Context, id string) error {
	n, err := sqlc.New().ExecutionEnvironmentDeleted(ctx, s.pool, mustPgUUID(id))
	return environmentPublication(n, err)
}
