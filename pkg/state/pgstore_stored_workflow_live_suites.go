package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func liveSuiteRecordFromSQL(row sqlc.StoredWorkflowLiveSuite) (storedWorkflowLiveSuiteRecord, error) {
	record := storedWorkflowLiveSuiteRecord{ID: pgUUIDString(row.ID), AppID: pgUUIDString(row.AppID), Name: row.Name, CreatedAt: timeFromPgtype(row.CreatedAt), UpdatedAt: timeFromPgtype(row.UpdatedAt), Managed: row.Managed,
		Plan: api.SaveStoredWorkflowLiveSuiteRequest{ExpectedVersion: row.WorkflowVersion, ExpectedPublishedVersion: row.PublishedVersion, ExpectedDeploymentID: pgUUIDString(row.DeploymentID)}}
	err := json.Unmarshal(row.Cases, &record.Plan.Cases)
	return record, err
}

func getLiveSuiteSQL(ctx context.Context, db sqlc.DBTX, appID, name, id string) (storedWorkflowLiveSuiteRecord, error) {
	row, err := sqlc.New().GetStoredWorkflowLiveSuite(ctx, db, sqlc.GetStoredWorkflowLiveSuiteParams{AppID: mustPgUUID(appID), Name: name, ID: mustPgUUID(id)})
	if errors.Is(err, pgx.ErrNoRows) {
		return storedWorkflowLiveSuiteRecord{}, ErrNotFound
	}
	if err != nil {
		return storedWorkflowLiveSuiteRecord{}, err
	}
	return liveSuiteRecordFromSQL(row)
}

func liveSuiteViewSQL(ctx context.Context, db sqlc.DBTX, record storedWorkflowLiveSuiteRecord) (api.StoredWorkflowLiveSuite, error) {
	current, deployment, err := liveCurrentSQL(ctx, db, record.AppID, record.Name)
	if err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	return liveSuiteView(record, current, deployment, func(id string) (api.StoredWorkflowLiveVerification, error) {
		row, err := sqlc.New().GetStoredWorkflowLiveVerification(ctx, db, sqlc.GetStoredWorkflowLiveVerificationParams{AppID: mustPgUUID(record.AppID), Name: record.Name, RunID: mustPgUUID(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			return api.StoredWorkflowLiveVerification{}, ErrNotFound
		}
		if err != nil {
			return api.StoredWorkflowLiveVerification{}, err
		}
		return liveViewSQL(ctx, db, row, current, deployment)
	})
}

func (s *PgStore) SaveStoredWorkflowLiveSuite(ctx context.Context, appID, name, id string, request api.SaveStoredWorkflowLiveSuiteRequest) (api.StoredWorkflowLiveSuite, error) {
	request = cloneLiveSuiteRequest(request)
	id, err := validateLiveSuite(id, &request)
	if err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.LockWorkflowRunAdmission(ctx, tx, appID); err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	prior, err := getLiveSuiteSQL(ctx, tx, appID, name, id)
	exists := err == nil
	if err != nil && !errors.Is(err, ErrNotFound) {
		return api.StoredWorkflowLiveSuite{}, err
	}
	if exists && prior.Managed {
		return api.StoredWorkflowLiveSuite{}, ErrWorkflowRunIdempotencyConflict
	}
	if exists {
		request, err = mergeLiveSuitePlan(prior.Plan, request)
	} else {
		var current StoredWorkflow
		var deployment string
		current, deployment, err = liveCurrentSQL(ctx, tx, appID, name)
		if err == nil {
			err = validateLiveSuiteTarget(request, current, deployment)
		}
	}
	if err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	if !exists {
		if err := liveSuiteRoomSQL(ctx, tx, appID, name); err != nil {
			return api.StoredWorkflowLiveSuite{}, err
		}
	}
	if err := validateLiveSuiteLinksSQL(ctx, tx, appID, name, request, prior, exists); err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	record, err := persistLiveSuiteSQL(ctx, tx, appID, name, id, request, prior, exists, false)
	if err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	view, err := liveSuiteViewSQL(ctx, tx, record)
	if err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	if err := q.PruneStoredWorkflowLiveSuites(ctx, tx, sqlc.PruneStoredWorkflowLiveSuitesParams{AppID: mustPgUUID(appID), Name: name, Retained: api.StoredWorkflowLiveSuiteRetainedLimit, ProtectedSince: pgtypeTimestamptzFromTime(time.Now().Add(-api.StoredWorkflowLiveSuiteProtectedWindow))}); err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	return view, nil
}

func validateLiveSuiteLinksSQL(ctx context.Context, db sqlc.DBTX, appID, name string, request api.SaveStoredWorkflowLiveSuiteRequest, prior storedWorkflowLiveSuiteRecord, exists bool) error {
	for i, member := range request.Cases {
		if member.RunID == "" || exists && prior.Plan.Cases[i].RunID == member.RunID {
			continue
		}
		row, err := sqlc.New().GetStoredWorkflowLiveVerification(ctx, db, sqlc.GetStoredWorkflowLiveVerificationParams{AppID: mustPgUUID(appID), Name: name, RunID: mustPgUUID(member.RunID)})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		live, err := liveRecordFromSQL(row)
		if err != nil {
			return err
		}
		if err := validateLiveSuiteLink(request, member, live.View); err != nil {
			return err
		}
	}
	return nil
}

func persistLiveSuiteSQL(ctx context.Context, db sqlc.DBTX, appID, name, id string, request api.SaveStoredWorkflowLiveSuiteRequest, prior storedWorkflowLiveSuiteRecord, exists, managed bool) (storedWorkflowLiveSuiteRecord, error) {
	if exists && liveSuitePlanEqual(prior.Plan, request) {
		return prior, nil
	}
	raw, err := json.Marshal(request.Cases)
	if err != nil {
		return prior, err
	}
	q := sqlc.New()
	var row sqlc.StoredWorkflowLiveSuite
	if exists {
		row, err = q.UpdateStoredWorkflowLiveSuite(ctx, db, sqlc.UpdateStoredWorkflowLiveSuiteParams{AppID: mustPgUUID(appID), Name: name, ID: mustPgUUID(id), Cases: raw})
	} else {
		row, err = q.InsertStoredWorkflowLiveSuite(ctx, db, sqlc.InsertStoredWorkflowLiveSuiteParams{ID: mustPgUUID(id), AppID: mustPgUUID(appID), Name: name, WorkflowVersion: request.ExpectedVersion, PublishedVersion: request.ExpectedPublishedVersion, DeploymentID: mustPgUUID(request.ExpectedDeploymentID), Cases: raw, Managed: managed})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return prior, ErrNotFound
	}
	if err != nil {
		return prior, err
	}
	return liveSuiteRecordFromSQL(row)
}

func (s *PgStore) GetStoredWorkflowLiveSuite(ctx context.Context, appID, name, id string) (api.StoredWorkflowLiveSuite, error) {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil {
		return api.StoredWorkflowLiveSuite{}, ErrNotFound
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	record, err := getLiveSuiteSQL(ctx, tx, appID, name, parsed.String())
	if err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	return liveSuiteViewSQL(ctx, tx, record)
}

func (s *PgStore) ListStoredWorkflowLiveSuites(ctx context.Context, appID, name string) (api.ListStoredWorkflowLiveSuitesResponse, error) {
	result := api.ListStoredWorkflowLiveSuitesResponse{Suites: []api.StoredWorkflowLiveSuiteSummary{}}
	if _, _, err := liveCurrentSQL(ctx, s.pool, appID, name); err != nil {
		return result, err
	}
	rows, err := sqlc.New().ListStoredWorkflowLiveSuites(ctx, s.pool, sqlc.ListStoredWorkflowLiveSuitesParams{AppID: mustPgUUID(appID), Name: name, Limit: api.StoredWorkflowLiveSuiteHistoryLimit})
	if err != nil {
		return result, err
	}
	for _, row := range rows {
		record, err := liveSuiteRecordFromSQL(row)
		if err != nil {
			return result, err
		}
		result.Suites = append(result.Suites, liveSuiteSummary(record))
	}
	return result, nil
}
