package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
)

func setupRow(r sqlc.DataPipelineSetup) (Setup, error) {
	result := Setup{ID: uid(r.ID), AccountID: uid(r.AccountID), DatabaseID: uid(r.DatabaseID), MirrorName: r.MirrorName, PipelineID: r.PipelineID, State: r.State, Phase: r.Phase, Reason: r.Reason, Revision: r.Revision, LeaseToken: r.LeaseToken, LeaseUntil: r.LeaseUntil.Time, CreatedAt: stamp(r.CreatedAt), UpdatedAt: stamp(r.UpdatedAt)}
	if json.Unmarshal(r.Request, &result.Request) != nil || json.Unmarshal(r.MirrorPlan, &result.MirrorPlan) != nil || json.Unmarshal(r.DataApiPlan, &result.DataAPIPlan) != nil {
		return Setup{}, ErrUnavailable
	}
	return result, nil
}
func (s *PostgresStore) SetupByName(ctx context.Context, account, name string) (Setup, error) {
	r, err := s.q.PipelineSetupByName(ctx, s.pool, sqlc.PipelineSetupByNameParams{AccountID: account, Name: name})
	if err != nil {
		return Setup{}, storeError(err)
	}
	return setupRow(r)
}
func (s *PostgresStore) Setup(ctx context.Context, account, id string) (Setup, error) {
	if !validID(account) || !validID(id) {
		return Setup{}, ErrNotFound
	}
	r, err := s.q.PipelineSetupGet(ctx, s.pool, sqlc.PipelineSetupGetParams{AccountID: account, ID: id})
	if err != nil {
		return Setup{}, storeError(err)
	}
	return setupRow(r)
}
func (s *PostgresStore) Setups(ctx context.Context, account string) ([]Setup, error) {
	rows, err := s.q.PipelineSetupList(ctx, s.pool, sqlc.PipelineSetupListParams{AccountID: account, PageLimit: api.DataPipelineSetupHistoryMax})
	if err != nil {
		return nil, storeError(err)
	}
	items := make([]Setup, 0, len(rows))
	for _, r := range rows {
		item, err := setupRow(r)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}
func (s *PostgresStore) EnqueueSetup(ctx context.Context, job Setup) (Setup, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Setup{}, storeError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = s.q.PipelineAccountLock(ctx, tx, job.AccountID); err != nil {
		return Setup{}, storeError(err)
	}
	old, err := s.q.PipelineSetupByName(ctx, tx, sqlc.PipelineSetupByNameParams{AccountID: job.AccountID, Name: job.Request.Name})
	if err == nil {
		existing, err := setupRow(old)
		if err != nil {
			return Setup{}, err
		}
		if !reflect.DeepEqual(existing.Request, job.Request) {
			return Setup{}, ErrConflict
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Setup{}, storeError(err)
	}

	if _, err = s.q.PipelineGetByName(ctx, tx, sqlc.PipelineGetByNameParams{AccountID: job.AccountID, Name: job.Request.Name}); err == nil {
		return Setup{}, ErrConflict
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Setup{}, storeError(err)
	}
	count, err := s.q.PipelineSetupCount(ctx, tx, job.AccountID)
	if err != nil {
		return Setup{}, storeError(err)
	}
	if count >= api.DataPipelineSetupsMax {
		return Setup{}, &QuotaError{Limit: api.DataPipelineSetupsMax, Observed: count}
	}
	if err = s.q.PipelineSetupPrune(ctx, tx, sqlc.PipelineSetupPruneParams{AccountID: job.AccountID, HistoryOffset: api.DataPipelineSetupHistoryMax - api.DataPipelineSetupsMax - 1, PageLimit: api.DataPipelineSetupHistoryMax}); err != nil {
		return Setup{}, storeError(err)
	}

	total, err := s.q.PipelineSetupTotalCount(ctx, tx, job.AccountID)
	if err != nil {
		return Setup{}, storeError(err)
	}
	if total >= api.DataPipelineSetupHistoryMax {
		return Setup{}, &QuotaError{Limit: api.DataPipelineSetupHistoryMax, Observed: total}
	}
	pipelines, err := s.q.PipelineCount(ctx, tx, job.AccountID)
	if err != nil {
		return Setup{}, storeError(err)
	}
	pending, err := s.q.PipelineSetupUnstartedCount(ctx, tx, job.AccountID)
	if err != nil {
		return Setup{}, storeError(err)
	}
	if pipelines+pending >= api.DataPipelinesMax {
		return Setup{}, &QuotaError{Limit: api.DataPipelinesMax, Observed: pipelines + pending}
	}
	request, _ := json.Marshal(job.Request)
	plan, _ := json.Marshal(job.MirrorPlan)
	apiPlan, _ := json.Marshal(job.DataAPIPlan)
	r, err := s.q.PipelineSetupInsert(ctx, tx, sqlc.PipelineSetupInsertParams{ID: job.ID, AccountID: job.AccountID, ConnectionID: job.Request.SourceConnectionID, Name: job.Request.Name, Request: request, MirrorPlan: plan, DataApiPlan: apiPlan, DatabaseID: job.DatabaseID, MirrorName: job.MirrorName})
	if err != nil {
		return Setup{}, storeError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return Setup{}, storeError(err)
	}
	return setupRow(r)
}
func (s *PostgresStore) ControlSetup(ctx context.Context, account, id string, revision int64, action string) (Setup, error) {
	if !validID(account) || !validID(id) || revision < 1 {
		return Setup{}, ErrInvalid
	}
	state := "working"
	if action == "cancel" {
		state = "cancelled"
	} else if action != "retry" {
		return Setup{}, ErrInvalid
	}
	r, err := s.q.PipelineSetupControl(ctx, s.pool, sqlc.PipelineSetupControlParams{AccountID: account, ID: id, Revision: revision, State: state})
	if errors.Is(err, pgx.ErrNoRows) {
		return Setup{}, ErrConflict
	}
	if err != nil {
		return Setup{}, storeError(err)
	}
	return setupRow(r)
}
func (s *PostgresStore) ClaimSetup(ctx context.Context, token string) (Setup, error) {
	r, err := s.q.PipelineSetupClaim(ctx, s.pool, token)
	if err != nil {
		return Setup{}, storeError(err)
	}
	return setupRow(r)
}
func (s *PostgresStore) FinishSetup(ctx context.Context, job, next Setup) error {
	apiPlan, err := json.Marshal(next.DataAPIPlan)
	if err != nil {
		return ErrInvalid
	}
	n, err := s.q.PipelineSetupFinish(ctx, s.pool, sqlc.PipelineSetupFinishParams{AccountID: job.AccountID, ID: job.ID, Revision: job.Revision, LeaseToken: job.LeaseToken, State: next.State, Phase: next.Phase, Reason: next.Reason, PipelineID: next.PipelineID, DataApiPlan: apiPlan})
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		_ = s.q.PipelineSetupRelease(ctx, s.pool, sqlc.PipelineSetupReleaseParams{ID: job.ID, LeaseToken: job.LeaseToken})
		return ErrConflict
	}
	return nil
}
