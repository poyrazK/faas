package ci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci/db/sqlc"
)

type PostgresStore struct {
	pool           *pgxpool.Pool
	q              sqlc.Queries
	executionClock func() time.Time // tests only; production leases use PostgreSQL's clock.
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore { return &PostgresStore{pool: pool} }

func dbUUID(value string) pgtype.UUID {
	id, err := uuid.Parse(value)
	return pgtype.UUID{Bytes: id, Valid: err == nil}
}

func uuidString(value pgtype.UUID) string { return uuid.UUID(value.Bytes).String() }

func storeError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("CI metadata: %w", err)
	}
	return nil
}

func revisionRow(row sqlc.CiPipelineRevision) (Revision, error) {
	out := Revision{ID: uuidString(row.ID), AccountID: uuidString(row.AccountID), Name: row.Name, Digest: row.Digest, CreatedAt: row.CreatedAt.Time}
	if err := json.Unmarshal(row.Definition, &out.Definition); err != nil {
		return Revision{}, fmt.Errorf("decode CI revision: %w", err)
	}
	return out, nil
}

func runRow(row sqlc.CiRun) (Run, error) {
	out := Run{ID: uuidString(row.ID), AccountID: uuidString(row.AccountID), Pipeline: row.Pipeline,
		RevisionID: uuidString(row.RevisionID), CommitSHA: row.CommitSha, Status: row.Status,
		BlockedReason: row.BlockedReason, CreatedAt: row.CreatedAt.Time}
	if row.FinishedAt.Valid {
		out.FinishedAt = &row.FinishedAt.Time
	}
	if err := json.Unmarshal(row.Plan, &out.Plan); err != nil {
		return Run{}, fmt.Errorf("decode CI plan: %w", err)
	}
	if len(row.GithubSource) != 0 {
		if err := json.Unmarshal(row.GithubSource, &out.GitHub); err != nil {
			return Run{}, fmt.Errorf("decode CI GitHub source: %w", err)
		}
	}
	return out, nil
}

func (s *PostgresStore) accountTx(ctx context.Context, accountID string) (pgx.Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, storeError(err)
	}
	if _, err := s.q.LockCIAccount(ctx, tx, dbUUID(accountID)); err != nil {
		_ = tx.Rollback(ctx)
		return nil, storeError(err)
	}
	return tx, nil
}

func (s *PostgresStore) PutRevision(ctx context.Context, accountID, name string, def Definition) (Revision, error) {
	if !ValidName(name) {
		return Revision{}, fmt.Errorf("invalid CI pipeline name")
	}
	plan, canonical, err := Compile(def)
	if err != nil {
		return Revision{}, err
	}
	tx, err := s.accountTx(ctx, accountID)
	if err != nil {
		return Revision{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := s.q.GetCIRevisionByDigest(ctx, tx, sqlc.GetCIRevisionByDigestParams{AccountID: dbUUID(accountID), Name: name, Digest: plan.DefinitionDigest})
	if err == nil {
		return revisionRow(row)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Revision{}, storeError(err)
	}
	count, err := s.q.CountCIRevisions(ctx, tx, dbUUID(accountID))
	if err != nil {
		return Revision{}, storeError(err)
	}
	if count >= api.CIMaxRevisionsPerAccount {
		return Revision{}, &LimitError{Resource: "revisions", Limit: api.CIMaxRevisionsPerAccount, Observed: int(count) + 1}
	}
	row, err = s.q.InsertCIRevision(ctx, tx, sqlc.InsertCIRevisionParams{ID: dbUUID(uuid.NewString()), AccountID: dbUUID(accountID), Name: name, Digest: plan.DefinitionDigest, Definition: canonical})
	if err != nil {
		return Revision{}, storeError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Revision{}, storeError(err)
	}
	return revisionRow(row)
}

func (s *PostgresStore) Revision(ctx context.Context, accountID, id string) (Revision, error) {
	row, err := s.q.GetCIRevision(ctx, s.pool, sqlc.GetCIRevisionParams{AccountID: dbUUID(accountID), ID: dbUUID(id)})
	if err != nil {
		return Revision{}, storeError(err)
	}
	return revisionRow(row)
}

func (s *PostgresStore) ListRevisions(ctx context.Context, accountID, name string) ([]Revision, error) {
	rows, err := s.q.ListCIRevisions(ctx, s.pool, sqlc.ListCIRevisionsParams{AccountID: dbUUID(accountID), Pipeline: name, MaxRows: api.CIMaxRevisionsPerAccount})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]Revision, 0, len(rows))
	for _, row := range rows {
		revision, err := revisionRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, revision)
	}
	return out, nil
}

func (s *PostgresStore) Submit(ctx context.Context, accountID, key string, req SubmitRequest) (Run, bool, error) {
	if err := validateSubmission(key, req); err != nil {
		return Run{}, false, err
	}
	tx, err := s.accountTx(ctx, accountID)
	if err != nil {
		return Run{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	existing, err := s.q.GetCIRunByKey(ctx, tx, sqlc.GetCIRunByKeyParams{AccountID: dbUUID(accountID), IdempotencyKey: key})
	if err == nil {
		if uuidString(existing.RevisionID) != req.RevisionID || existing.CommitSha != req.CommitSHA {
			return Run{}, false, ErrConflict
		}
		row, err := runRow(existing)
		return row, true, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Run{}, false, storeError(err)
	}
	revision, err := s.q.GetCIRevision(ctx, tx, sqlc.GetCIRevisionParams{AccountID: dbUUID(accountID), ID: dbUUID(req.RevisionID)})
	if err != nil {
		return Run{}, false, storeError(err)
	}
	count, err := s.q.CountCIRuns(ctx, tx, dbUUID(accountID))
	if err != nil {
		return Run{}, false, storeError(err)
	}
	if count >= api.CIMaxRunsPerAccount {
		return Run{}, false, &LimitError{Resource: "runs", Limit: api.CIMaxRunsPerAccount, Observed: int(count) + 1}
	}
	return s.insertRun(ctx, tx, accountID, key, req, revision)
}

func (s *PostgresStore) insertRun(ctx context.Context, tx pgx.Tx, accountID, key string, req SubmitRequest, revision sqlc.CiPipelineRevision) (Run, bool, error) {
	row, err := s.insertRunRecord(ctx, tx, accountID, key, req, revision)
	if err != nil {
		return Run{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Run{}, false, storeError(err)
	}
	out, err := runRow(row)
	return out, false, err
}

func (s *PostgresStore) insertRunRecord(ctx context.Context, tx pgx.Tx, accountID, key string, req SubmitRequest, revision sqlc.CiPipelineRevision) (sqlc.CiRun, error) {
	decoded, err := revisionRow(revision)
	if err != nil {
		return sqlc.CiRun{}, err
	}
	plan, _, err := Compile(decoded.Definition)
	if err != nil {
		return sqlc.CiRun{}, err
	}
	if plan.DefinitionDigest != revision.Digest {
		return sqlc.CiRun{}, fmt.Errorf("CI revision digest mismatch")
	}
	data, err := json.Marshal(plan)
	if err != nil {
		return sqlc.CiRun{}, fmt.Errorf("encode CI plan: %w", err)
	}
	row, err := s.q.InsertCIRun(ctx, tx, sqlc.InsertCIRunParams{ID: dbUUID(uuid.NewString()), AccountID: dbUUID(accountID), RevisionID: revision.ID, Pipeline: revision.Name, CommitSha: req.CommitSHA, IdempotencyKey: key, Plan: data})
	if err != nil {
		return sqlc.CiRun{}, storeError(err)
	}
	if err := s.q.InsertCIEvent(ctx, tx, sqlc.InsertCIEventParams{AccountID: row.AccountID, RunID: row.ID, Sequence: 1, EventType: StatusBlocked, CreatedAt: row.CreatedAt}); err != nil {
		return sqlc.CiRun{}, storeError(err)
	}
	return row, nil
}

func (s *PostgresStore) Run(ctx context.Context, accountID, id string) (Run, error) {
	row, err := s.q.GetCIRun(ctx, s.pool, sqlc.GetCIRunParams{AccountID: dbUUID(accountID), ID: dbUUID(id)})
	if err != nil {
		return Run{}, storeError(err)
	}
	return runRow(row)
}

func (s *PostgresStore) ListRuns(ctx context.Context, accountID string) ([]Run, error) {
	rows, err := s.q.ListCIRuns(ctx, s.pool, sqlc.ListCIRunsParams{AccountID: dbUUID(accountID), MaxRows: api.CIMaxRunsPerAccount})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]Run, 0, len(rows))
	for _, row := range rows {
		run, err := runRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, nil
}

func (s *PostgresStore) Cancel(ctx context.Context, accountID, id string) (Run, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Run{}, storeError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := s.q.LockCIRun(ctx, tx, sqlc.LockCIRunParams{AccountID: dbUUID(accountID), ID: dbUUID(id)})
	if err != nil {
		return Run{}, storeError(err)
	}
	if row.Status == StatusCancelled {
		return runRow(row)
	}
	row, err = s.q.CancelCIRun(ctx, tx, sqlc.CancelCIRunParams{AccountID: dbUUID(accountID), ID: dbUUID(id)})
	if err != nil {
		return Run{}, storeError(err)
	}
	if err := s.q.InsertCIEvent(ctx, tx, sqlc.InsertCIEventParams{AccountID: row.AccountID, RunID: row.ID, Sequence: 2, EventType: StatusCancelled, CreatedAt: row.FinishedAt}); err != nil {
		return Run{}, storeError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Run{}, storeError(err)
	}
	return runRow(row)
}

func (s *PostgresStore) Events(ctx context.Context, accountID, id string) ([]Event, error) {
	if _, err := s.Run(ctx, accountID, id); err != nil {
		return nil, err
	}
	rows, err := s.q.ListCIEvents(ctx, s.pool, sqlc.ListCIEventsParams{AccountID: dbUUID(accountID), RunID: dbUUID(id), MaxRows: api.CIEventListMax})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]Event, 0, len(rows))
	for _, row := range rows {
		out = append(out, Event{Sequence: int(row.Sequence), Type: row.EventType, At: row.CreatedAt.Time})
	}
	return out, nil
}

var _ Store = (*PostgresStore)(nil)
var _ Store = (*MemoryStore)(nil)
