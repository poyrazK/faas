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

func liveSuiteRecordFromSQL(row sqlc.WorkflowAutomationLiveSuite) (automationLiveSuiteRecord, error) {
	record := automationLiveSuiteRecord{ID: pgUUIDString(row.ID), AppID: pgUUIDString(row.AppID), Name: row.Name, CreatedAt: timeFromPgtype(row.CreatedAt), UpdatedAt: timeFromPgtype(row.UpdatedAt), Managed: row.Managed,
		Plan: api.SaveAutomationLiveSuiteRequest{ExpectedVersion: row.AutomationVersion, ExpectedPublishedVersion: row.PublishedVersion, ExpectedDeploymentID: pgUUIDString(row.DeploymentID)}}
	err := json.Unmarshal(row.Cases, &record.Plan.Cases)
	return record, err
}

func getLiveSuiteSQL(ctx context.Context, db sqlc.DBTX, appID, name, id string) (automationLiveSuiteRecord, error) {
	row, err := sqlc.New().GetAutomationLiveSuite(ctx, db, sqlc.GetAutomationLiveSuiteParams{AppID: mustPgUUID(appID), Name: name, ID: mustPgUUID(id)})
	if errors.Is(err, pgx.ErrNoRows) {
		return automationLiveSuiteRecord{}, ErrNotFound
	}
	if err != nil {
		return automationLiveSuiteRecord{}, err
	}
	return liveSuiteRecordFromSQL(row)
}

func liveSuiteViewSQL(ctx context.Context, db sqlc.DBTX, record automationLiveSuiteRecord) (api.AutomationLiveSuite, error) {
	current, deployment, err := liveCurrentSQL(ctx, db, record.AppID, record.Name)
	if err != nil {
		return api.AutomationLiveSuite{}, err
	}
	return liveSuiteView(record, current, deployment, func(id string) (api.AutomationLiveVerification, error) {
		row, err := sqlc.New().GetAutomationLiveVerification(ctx, db, sqlc.GetAutomationLiveVerificationParams{AppID: mustPgUUID(record.AppID), Name: record.Name, RunID: mustPgUUID(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			return api.AutomationLiveVerification{}, ErrNotFound
		}
		if err != nil {
			return api.AutomationLiveVerification{}, err
		}
		return liveViewSQL(ctx, db, row, current, deployment)
	})
}

func (s *PgStore) SaveAutomationLiveSuite(ctx context.Context, appID, name, id string, request api.SaveAutomationLiveSuiteRequest) (api.AutomationLiveSuite, error) {
	request = cloneLiveSuiteRequest(request)
	id, err := validateLiveSuite(id, &request)
	if err != nil {
		return api.AutomationLiveSuite{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.AutomationLiveSuite{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.LockWorkflowRunAdmission(ctx, tx, appID); err != nil {
		return api.AutomationLiveSuite{}, err
	}
	prior, err := getLiveSuiteSQL(ctx, tx, appID, name, id)
	exists := err == nil
	if err != nil && !errors.Is(err, ErrNotFound) {
		return api.AutomationLiveSuite{}, err
	}
	if exists && prior.Managed {
		return api.AutomationLiveSuite{}, ErrWorkflowRunIdempotencyConflict
	}
	if exists {
		request, err = mergeLiveSuitePlan(prior.Plan, request)
	} else {
		var current Automation
		var deployment string
		current, deployment, err = liveCurrentSQL(ctx, tx, appID, name)
		if err == nil {
			err = validateLiveSuiteTarget(request, current, deployment)
		}
	}
	if err != nil {
		return api.AutomationLiveSuite{}, err
	}
	if !exists {
		if err := liveSuiteRoomSQL(ctx, tx, appID, name); err != nil {
			return api.AutomationLiveSuite{}, err
		}
	}
	if err := validateLiveSuiteLinksSQL(ctx, tx, appID, name, request, prior, exists); err != nil {
		return api.AutomationLiveSuite{}, err
	}
	record, err := persistLiveSuiteSQL(ctx, tx, appID, name, id, request, prior, exists, false)
	if err != nil {
		return api.AutomationLiveSuite{}, err
	}
	view, err := liveSuiteViewSQL(ctx, tx, record)
	if err != nil {
		return api.AutomationLiveSuite{}, err
	}
	if err := q.PruneAutomationLiveSuites(ctx, tx, sqlc.PruneAutomationLiveSuitesParams{AppID: mustPgUUID(appID), Name: name, Retained: api.AutomationLiveSuiteRetainedLimit, ProtectedSince: pgtypeTimestamptzFromTime(time.Now().Add(-api.AutomationLiveSuiteStartWindow))}); err != nil {
		return api.AutomationLiveSuite{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return api.AutomationLiveSuite{}, err
	}
	return view, nil
}

func validateLiveSuiteLinksSQL(ctx context.Context, db sqlc.DBTX, appID, name string, request api.SaveAutomationLiveSuiteRequest, prior automationLiveSuiteRecord, exists bool) error {
	for i, member := range request.Cases {
		if member.RunID == "" || exists && prior.Plan.Cases[i].RunID == member.RunID {
			continue
		}
		row, err := sqlc.New().GetAutomationLiveVerification(ctx, db, sqlc.GetAutomationLiveVerificationParams{AppID: mustPgUUID(appID), Name: name, RunID: mustPgUUID(member.RunID)})
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

func persistLiveSuiteSQL(ctx context.Context, db sqlc.DBTX, appID, name, id string, request api.SaveAutomationLiveSuiteRequest, prior automationLiveSuiteRecord, exists, managed bool) (automationLiveSuiteRecord, error) {
	if exists && liveSuitePlanEqual(prior.Plan, request) {
		return prior, nil
	}
	raw, err := json.Marshal(request.Cases)
	if err != nil {
		return prior, err
	}
	q := sqlc.New()
	var row sqlc.WorkflowAutomationLiveSuite
	if exists {
		row, err = q.UpdateAutomationLiveSuite(ctx, db, sqlc.UpdateAutomationLiveSuiteParams{AppID: mustPgUUID(appID), Name: name, ID: mustPgUUID(id), Cases: raw})
	} else {
		row, err = q.InsertAutomationLiveSuite(ctx, db, sqlc.InsertAutomationLiveSuiteParams{ID: mustPgUUID(id), AppID: mustPgUUID(appID), Name: name, AutomationVersion: request.ExpectedVersion, PublishedVersion: request.ExpectedPublishedVersion, DeploymentID: mustPgUUID(request.ExpectedDeploymentID), Cases: raw, Managed: managed})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return prior, ErrNotFound
	}
	if err != nil {
		return prior, err
	}
	return liveSuiteRecordFromSQL(row)
}

func (s *PgStore) GetAutomationLiveSuite(ctx context.Context, appID, name, id string) (api.AutomationLiveSuite, error) {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil {
		return api.AutomationLiveSuite{}, ErrNotFound
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.AutomationLiveSuite{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	record, err := getLiveSuiteSQL(ctx, tx, appID, name, parsed.String())
	if err != nil {
		return api.AutomationLiveSuite{}, err
	}
	return liveSuiteViewSQL(ctx, tx, record)
}

func (s *PgStore) ListAutomationLiveSuites(ctx context.Context, appID, name string) (api.ListAutomationLiveSuitesResponse, error) {
	result := api.ListAutomationLiveSuitesResponse{Suites: []api.AutomationLiveSuiteSummary{}}
	if _, _, err := liveCurrentSQL(ctx, s.pool, appID, name); err != nil {
		return result, err
	}
	rows, err := sqlc.New().ListAutomationLiveSuites(ctx, s.pool, sqlc.ListAutomationLiveSuitesParams{AppID: mustPgUUID(appID), Name: name, Limit: api.AutomationLiveSuiteHistoryLimit})
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
