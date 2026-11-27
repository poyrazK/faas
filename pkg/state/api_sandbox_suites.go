package state

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type APISandboxSuiteRun struct {
	api.APISandboxSuiteRunSummary
	AppID         string
	Pins          APISandboxApprovalPins
	DeadlineAt    time.Time
	NextAttemptAt *time.Time
	LeaseToken    string
	LeaseUntil    time.Time
	FailureCode   string
}

type APISandboxSuiteScenario struct {
	api.APISandboxSuiteScenarioResult
	ComparisonID string
	definition   []byte
}

func (item APISandboxSuiteScenario) Scenario() (api.APISandboxScenario, error) {
	return (savedAPISandboxScenario{revision: item.ScenarioRevision, definition: item.definition}).decode()
}

type APISandboxSuiteStore interface {
	GetAPISandboxSuite(context.Context, string) (api.APISandboxSuite, error)
	PutAPISandboxSuite(context.Context, string, string, []string, *bool) (api.APISandboxSuite, error)
	CreateAPISandboxSuiteRun(context.Context, APISandboxSuiteRun) (APISandboxSuiteRun, error)
	GetAPISandboxSuiteRun(context.Context, string) (APISandboxSuiteRun, error)
	ListAPISandboxSuiteRuns(context.Context, string) ([]api.APISandboxSuiteRunSummary, error)
	ListAPISandboxSuiteScenarios(context.Context, string) ([]APISandboxSuiteScenario, error)
	GetAPISandboxSuiteScenario(context.Context, string, int) (APISandboxSuiteScenario, error)
	ClaimAPISandboxSuiteRun(context.Context, time.Time, time.Duration) (APISandboxSuiteRun, error)
	CheckpointAPISandboxSuiteRun(context.Context, APISandboxSuiteRun, time.Time) error
}

var ErrAPISandboxSuiteAlreadyActive = fmt.Errorf("%w: app has an active sandbox suite run", ErrConflict)

func pgSandboxSuite(revision pgtype.UUID, ids []pgtype.UUID, automatic bool) api.APISandboxSuite {
	suite := api.APISandboxSuite{Revision: pgUUIDString(revision), ScenarioIDs: make([]string, len(ids)), RunBeforeApproval: automatic}
	for i, id := range ids {
		suite.ScenarioIDs[i] = pgUUIDString(id)
	}
	return suite
}

func pgSandboxSuiteIDs(ids []string) []pgtype.UUID {
	out := make([]pgtype.UUID, len(ids))
	for i, id := range ids {
		out[i] = mustPgUUID(id)
	}
	return out
}

func (s *PgStore) GetAPISandboxSuite(ctx context.Context, appID string) (api.APISandboxSuite, error) {
	row, err := sqlc.New().GetAPISandboxSuite(ctx, s.pool, mustPgUUID(appID))
	return pgSandboxSuite(row.Revision, row.ScenarioIds, row.RunBeforeApproval), mapErr(err)
}

func updateSandboxSuite(current api.APISandboxSuite, expected string, ids []string, automatic *bool) (api.APISandboxSuite, error) {
	enabled := current.RunBeforeApproval
	if automatic != nil {
		enabled = *automatic
	}
	enabled = enabled && len(ids) != 0
	if current.Revision != "" && enabled == current.RunBeforeApproval && slices.Equal(ids, current.ScenarioIDs) {
		return current, nil
	}
	if current.Revision != expected {
		return api.APISandboxSuite{}, ErrConflict
	}
	return api.APISandboxSuite{Revision: uuid.NewString(), ScenarioIDs: append([]string{}, ids...), RunBeforeApproval: enabled}, nil
}

func (s *PgStore) PutAPISandboxSuite(ctx context.Context, appID, expected string, ids []string, automatic *bool) (api.APISandboxSuite, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.APISandboxSuite{}, err
	}
	defer rollbackOrgAccess(ctx, tx)
	q, key := sqlc.New(), mustPgUUID(appID)
	if _, err := q.LockAPISandboxScenarioLibrary(ctx, tx, key); err != nil {
		return api.APISandboxSuite{}, mapErr(err)
	}
	row, err := q.GetAPISandboxSuite(ctx, tx, key)
	if err != nil && !errors.Is(mapErr(err), ErrNotFound) {
		return api.APISandboxSuite{}, mapErr(err)
	}
	current := pgSandboxSuite(row.Revision, row.ScenarioIds, row.RunBeforeApproval)
	updated, err := updateSandboxSuite(current, expected, ids, automatic)
	if err != nil || updated.Revision == current.Revision {
		return updated, err
	}
	scenarioIDs := pgSandboxSuiteIDs(ids)
	entries, err := q.APISandboxSuiteLibraryEntries(ctx, tx, sqlc.APISandboxSuiteLibraryEntriesParams{AppID: key, ScenarioIds: scenarioIDs})
	if err != nil {
		return api.APISandboxSuite{}, mapErr(err)
	}
	if len(entries) != len(ids) {
		return api.APISandboxSuite{}, ErrConflict
	}
	if err := q.PutAPISandboxSuite(ctx, tx, sqlc.PutAPISandboxSuiteParams{
		AppID: key, Revision: mustPgUUID(updated.Revision), ScenarioIds: scenarioIDs, RunBeforeApproval: updated.RunBeforeApproval,
	}); err != nil {
		return api.APISandboxSuite{}, mapErr(err)
	}
	return updated, mapErr(tx.Commit(ctx))
}

func (s *PgStore) CreateAPISandboxSuiteRun(ctx context.Context, run APISandboxSuiteRun) (APISandboxSuiteRun, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return APISandboxSuiteRun{}, err
	}
	defer rollbackOrgAccess(ctx, tx)
	q, key := sqlc.New(), mustPgUUID(run.AppID)
	if _, err := q.LockAPISandboxScenarioLibrary(ctx, tx, key); err != nil {
		return APISandboxSuiteRun{}, mapErr(err)
	}
	suite, err := q.GetAPISandboxSuite(ctx, tx, key)
	if err != nil && !errors.Is(mapErr(err), ErrNotFound) {
		return APISandboxSuiteRun{}, mapErr(err)
	}
	if pgUUIDString(suite.Revision) != run.SuiteRevision || len(suite.ScenarioIds) == 0 {
		return APISandboxSuiteRun{}, ErrConflict
	}
	if run.Automatic && !suite.RunBeforeApproval {
		policy, err := q.GetAppReleaseApprovalPolicy(ctx, tx, key)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return APISandboxSuiteRun{}, mapErr(err)
		}
		if !policy.RequirePassingSandboxSuite {
			return APISandboxSuiteRun{}, ErrConflict
		}
	}
	entries, err := q.APISandboxSuiteLibraryEntries(ctx, tx, sqlc.APISandboxSuiteLibraryEntriesParams{AppID: key, ScenarioIds: suite.ScenarioIds})
	if err != nil {
		return APISandboxSuiteRun{}, mapErr(err)
	}
	if len(entries) != len(suite.ScenarioIds) {
		return APISandboxSuiteRun{}, ErrConflict
	}
	row, err := q.CreateAPISandboxSuiteRun(ctx, tx, sqlc.CreateAPISandboxSuiteRunParams{
		ID: mustPgUUID(run.ID), AppID: key, SuiteRevision: suite.Revision,
		BaselineDeploymentID: mustPgUUID(run.BaselineDeploymentID), CandidateDeploymentID: mustPgUUID(run.CandidateDeploymentID),
		BaselineArtifactPin: run.Pins.Baseline, CandidateArtifactPin: run.Pins.Candidate,
		ScenarioCount: int16(len(entries)), CreatedAt: pgTimestamptzUTC(run.CreatedAt), DeadlineAt: pgTimestamptzUTC(run.DeadlineAt),
		Automatic: run.Automatic,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.ConstraintName == "api_sandbox_suite_runs_active_app" {
			return APISandboxSuiteRun{}, ErrAPISandboxSuiteAlreadyActive
		}
		return APISandboxSuiteRun{}, mapErr(err)
	}
	for i, entry := range entries {
		if err := q.CreateAPISandboxSuiteScenario(ctx, tx, sqlc.CreateAPISandboxSuiteScenarioParams{
			SuiteRunID: row.ID, Position: int16(i), ScenarioID: entry.ID, ScenarioRevision: entry.Revision,
			ScenarioName: entry.Name, Definition: entry.Definition, ComparisonID: mustPgUUID(uuid.NewString()),
		}); err != nil {
			return APISandboxSuiteRun{}, mapErr(err)
		}
	}
	return pgSandboxSuiteRun(row), mapErr(tx.Commit(ctx))
}

func pgSandboxSuiteRun(row sqlc.ApiSandboxSuiteRun) APISandboxSuiteRun {
	return APISandboxSuiteRun{
		APISandboxSuiteRunSummary: api.APISandboxSuiteRunSummary{
			ID: pgUUIDString(row.ID), SuiteRevision: pgUUIDString(row.SuiteRevision),
			BaselineDeploymentID: pgUUIDString(row.BaselineDeploymentID), CandidateDeploymentID: pgUUIDString(row.CandidateDeploymentID),
			ScenarioCount: int(row.ScenarioCount), CompletedCount: int(row.CompletedCount),
			CreatedAt: row.CreatedAt.Time.UTC(), FinishedAt: optionalPgTime(row.FinishedAt),
			Automatic: row.Automatic, WaitingForCapacity: row.WaitingForCapacity,
		},
		AppID: pgUUIDString(row.AppID), Pins: APISandboxApprovalPins{Baseline: row.BaselineArtifactPin, Candidate: row.CandidateArtifactPin},
		DeadlineAt: row.DeadlineAt.Time.UTC(), NextAttemptAt: optionalPgTime(row.NextAttemptAt),
		LeaseToken: pgUUIDString(row.LeaseToken), LeaseUntil: row.LeaseUntil.Time.UTC(), FailureCode: row.FailureCode,
	}
}

func (s *PgStore) GetAPISandboxSuiteRun(ctx context.Context, id string) (APISandboxSuiteRun, error) {
	row, err := sqlc.New().GetAPISandboxSuiteRun(ctx, s.pool, mustPgUUID(id))
	return pgSandboxSuiteRun(row), mapErr(err)
}

func (s *PgStore) ListAPISandboxSuiteRuns(ctx context.Context, candidateID string) ([]api.APISandboxSuiteRunSummary, error) {
	rows, err := sqlc.New().ListAPISandboxSuiteRuns(ctx, s.pool, mustPgUUID(candidateID))
	out := make([]api.APISandboxSuiteRunSummary, 0, len(rows))
	for _, row := range rows {
		out = append(out, pgSandboxSuiteRun(row).APISandboxSuiteRunSummary)
	}
	return out, mapErr(err)
}

func (s *PgStore) ListAPISandboxSuiteScenarios(ctx context.Context, id string) ([]APISandboxSuiteScenario, error) {
	rows, err := sqlc.New().ListAPISandboxSuiteScenarios(ctx, s.pool, mustPgUUID(id))
	out := make([]APISandboxSuiteScenario, 0, len(rows))
	for _, row := range rows {
		out = append(out, APISandboxSuiteScenario{
			APISandboxSuiteScenarioResult: api.APISandboxSuiteScenarioResult{
				ScenarioID: pgUUIDString(row.ScenarioID), ScenarioRevision: pgUUIDString(row.ScenarioRevision), ScenarioName: row.ScenarioName,
			},
			ComparisonID: pgUUIDString(row.ComparisonID),
		})
	}
	return out, mapErr(err)
}

func (s *PgStore) GetAPISandboxSuiteScenario(ctx context.Context, id string, pos int) (APISandboxSuiteScenario, error) {
	row, err := sqlc.New().GetAPISandboxSuiteScenario(ctx, s.pool, sqlc.GetAPISandboxSuiteScenarioParams{SuiteRunID: mustPgUUID(id), Position: int16(pos)})
	return APISandboxSuiteScenario{
		APISandboxSuiteScenarioResult: api.APISandboxSuiteScenarioResult{
			ScenarioID: pgUUIDString(row.ScenarioID), ScenarioRevision: pgUUIDString(row.ScenarioRevision), ScenarioName: row.ScenarioName,
		},
		ComparisonID: pgUUIDString(row.ComparisonID), definition: row.Definition,
	}, mapErr(err)
}

func (s *PgStore) ClaimAPISandboxSuiteRun(ctx context.Context, now time.Time, lease time.Duration) (APISandboxSuiteRun, error) {
	row, err := sqlc.New().ClaimAPISandboxSuiteRun(ctx, s.pool, sqlc.ClaimAPISandboxSuiteRunParams{
		ClaimedAt: pgTimestamptzUTC(now), ExpiresAt: pgTimestamptzUTC(now.Add(lease)), Token: mustPgUUID(uuid.NewString()),
	})
	return pgSandboxSuiteRun(row), mapErr(err)
}

func (s *PgStore) CheckpointAPISandboxSuiteRun(ctx context.Context, run APISandboxSuiteRun, now time.Time) error {
	var next, finished pgtype.Timestamptz
	if run.NextAttemptAt != nil {
		next = pgTimestamptzUTC(*run.NextAttemptAt)
	}
	if run.FinishedAt != nil {
		finished = pgTimestamptzUTC(*run.FinishedAt)
	}
	n, err := sqlc.New().CheckpointAPISandboxSuiteRun(ctx, s.pool, sqlc.CheckpointAPISandboxSuiteRunParams{
		ID: mustPgUUID(run.ID), Token: mustPgUUID(run.LeaseToken), SavedAt: pgTimestamptzUTC(now),
		CompletedCount: int16(run.CompletedCount), NextAttemptAt: next, FinishedAt: finished, FailureCode: run.FailureCode,
		WaitingForCapacity: run.WaitingForCapacity,
	})
	if err == nil && n == 0 {
		return ErrConflict
	}
	return mapErr(err)
}
