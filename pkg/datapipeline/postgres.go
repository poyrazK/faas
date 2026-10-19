package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/connectioncapacity"
	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
)

type PostgresStore struct {
	pool *pgxpool.Pool
	q    *sqlc.Queries
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool, q: sqlc.New()}
}

var _ Store = (*PostgresStore)(nil)

func storeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "PCC01":
			return connectioncapacity.ErrInsufficient
		case "PCC02":
			return connectioncapacity.ErrUnknown
		case "23505", "23503", "P0001":
			return ErrConflict
		case "23514", "22P02":
			return ErrInvalid
		}
	}
	return ErrUnavailable
}
func uid(id pgtype.UUID) string { return uuid.UUID(id.Bytes).String() }
func stamp(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.UTC().Format(time.RFC3339Nano)
}
func connectionRow(r sqlc.DataPipelineConnection) Connection {
	return Connection{DataConnection: api.DataConnection{ID: uid(r.ID), Name: r.Name, Revision: r.Revision, CreatedAt: stamp(r.CreatedAt)}, AccountID: uid(r.AccountID), Ciphertext: r.Ciphertext, Identity: r.Identity}
}
func pipelineRow(r sqlc.DataPipeline) Pipeline {
	p := Pipeline{AccountID: uid(r.AccountID), LeaseToken: r.LeaseToken, LeaseUntil: r.LeaseUntil.Time, VerifyRequested: r.VerifyRequested, Initialized: r.Initialized, DataPipeline: api.DataPipeline{AppliedThroughAt: stamp(r.AppliedThroughAt), ID: uid(r.ID), Name: r.Name, SourceConnectionID: uid(r.SourceConnectionID), DatabaseID: uid(r.DatabaseID), DesiredState: r.DesiredState, State: r.State, Revision: r.Revision, Generation: r.Generation, ObservedRevision: r.ObservedRevision, ObservationFresh: r.ObservedAt.Valid && r.ObservedRevision == r.Revision && time.Since(r.ObservedAt.Time) >= 0 && time.Since(r.ObservedAt.Time) < api.DataPipelineObservationMaxAge, Reason: r.Reason, TablesReady: int(r.TablesReady), ObservedAt: stamp(r.ObservedAt), Verification: r.Verification, VerifiedAt: stamp(r.VerifiedAt), CreatedAt: stamp(r.CreatedAt)}}
	_ = json.Unmarshal(r.Tables, &p.Tables)
	_ = json.Unmarshal(r.ReadSubjects, &p.ReadSubjects)
	if len(r.VerificationProgress) > 2 {
		_ = json.Unmarshal(r.VerificationProgress, &p.VerificationProgress)
	}
	if len(r.ReaderChange) > 2 {
		_ = json.Unmarshal(r.ReaderChange, &p.ReaderChange)
	}
	p.SchemaApproval = r.SchemaApproval
	if len(r.SchemaChange) > 2 {
		_ = json.Unmarshal(r.SchemaChange, &p.SchemaChange)
	}
	if r.RetainedWalBytes.Valid {
		v := r.RetainedWalBytes.Int64
		p.RetainedWALBytes = &v
	}
	p.Health = pipelineHealth(p.DataPipeline, time.Now())
	return p
}

func (s *PostgresStore) CreateConnection(ctx context.Context, c Connection) (Connection, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Connection{}, storeError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = s.q.PipelineAccountLock(ctx, tx, c.AccountID); err != nil {
		return Connection{}, storeError(err)
	}
	count, err := s.q.PipelineConnectionCount(ctx, tx, c.AccountID)
	if err != nil {
		return Connection{}, storeError(err)
	}
	if count >= api.DataPipelineConnectionsMax {
		return Connection{}, &QuotaError{Limit: api.DataPipelineConnectionsMax, Observed: count}
	}
	r, err := s.q.PipelineInsertConnection(ctx, tx, sqlc.PipelineInsertConnectionParams{ID: c.ID, AccountID: c.AccountID, Name: c.Name, Ciphertext: c.Ciphertext, Identity: c.Identity})
	if err != nil {
		return Connection{}, storeError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return Connection{}, storeError(err)
	}
	return connectionRow(r), nil
}
func (s *PostgresStore) Connections(ctx context.Context, account string) ([]Connection, error) {
	rows, err := s.q.PipelineConnections(ctx, s.pool, sqlc.PipelineConnectionsParams{AccountID: account, PageLimit: api.DataPipelineConnectionsMax})
	if err != nil {
		return nil, storeError(err)
	}
	items := make([]Connection, 0, len(rows))
	for _, r := range rows {
		items = append(items, connectionRow(r))
	}
	return items, nil
}
func (s *PostgresStore) Connection(ctx context.Context, account, id string) (Connection, error) {
	if !validID(id) || !validID(account) {
		return Connection{}, ErrNotFound
	}
	r, err := s.q.PipelineConnection(ctx, s.pool, sqlc.PipelineConnectionParams{AccountID: account, ID: id})
	return connectionRow(r), storeError(err)
}
func (s *PostgresStore) RotateConnection(ctx context.Context, c Connection, revision int64) (Connection, error) {
	if revision <= 0 {
		return Connection{}, ErrInvalid
	}
	r, err := s.q.PipelineRotateConnection(ctx, s.pool, sqlc.PipelineRotateConnectionParams{AccountID: c.AccountID, ID: c.ID, Revision: revision, Ciphertext: c.Ciphertext})
	if errors.Is(err, pgx.ErrNoRows) {
		return Connection{}, ErrConflict
	}
	return connectionRow(r), storeError(err)
}
func (s *PostgresStore) DeleteConnection(ctx context.Context, account, id string) error {
	if !validID(account) || !validID(id) {
		return ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return storeError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = s.q.PipelineAccountLock(ctx, tx, account); err != nil {
		return storeError(err)
	}

	if _, err = s.q.PipelineConnection(ctx, tx, sqlc.PipelineConnectionParams{AccountID: account, ID: id}); err != nil {
		return storeError(err)
	}
	n, err := s.q.PipelineDeleteConnection(ctx, tx, sqlc.PipelineDeleteConnectionParams{AccountID: account, ID: id})
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return storeError(tx.Commit(ctx))
}

func (s *PostgresStore) Create(ctx context.Context, p Pipeline) (Pipeline, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Pipeline{}, storeError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = s.q.PipelineAccountLock(ctx, tx, p.AccountID); err != nil {
		return Pipeline{}, storeError(err)
	}
	old, err := s.q.PipelineGetByName(ctx, tx, sqlc.PipelineGetByNameParams{AccountID: p.AccountID, Name: p.Name})
	if err == nil {
		existing := pipelineRow(old)
		if existing.SourceConnectionID != p.SourceConnectionID || existing.DatabaseID != p.DatabaseID || !slices.Equal(existing.Tables, p.Tables) || !slices.Equal(existing.ReadSubjects, p.ReadSubjects) || existing.DesiredState == "deleted" {
			return Pipeline{}, ErrConflict
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Pipeline{}, storeError(err)
	}
	savedSetup, setupErr := s.q.PipelineSetupByName(ctx, tx, sqlc.PipelineSetupByNameParams{AccountID: p.AccountID, Name: p.Name})
	if setupErr == nil && (savedSetup.State == "working" || savedSetup.State == "blocked") {
		saved, decodeErr := setupRow(savedSetup)
		if decodeErr != nil {
			return Pipeline{}, decodeErr
		}
		if saved.DatabaseID != p.DatabaseID || saved.Request.SourceConnectionID != p.SourceConnectionID || !slices.Equal(saved.Request.Tables, p.Tables) || !slices.Equal(saved.Request.ReadSubjects, p.ReadSubjects) {
			return Pipeline{}, ErrConflict
		}
	} else if setupErr != nil && !errors.Is(setupErr, pgx.ErrNoRows) {
		return Pipeline{}, storeError(setupErr)
	}
	if _, err = s.q.PipelineConnection(ctx, tx, sqlc.PipelineConnectionParams{AccountID: p.AccountID, ID: p.SourceConnectionID}); err != nil {
		return Pipeline{}, storeError(err)
	}
	checked, err := s.q.PipelineCheckForCreate(ctx, tx, sqlc.PipelineCheckForCreateParams{AccountID: p.AccountID, ConnectionID: p.SourceConnectionID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Pipeline{}, ErrCheckRequired
	}
	if err != nil {
		return Pipeline{}, storeError(err)
	}
	check, err := describeCheck(sqlc.PipelineCheckGetRow(checked))
	if err != nil {
		return Pipeline{}, err
	}
	if check.DatabaseID != "" && check.DatabaseID != p.DatabaseID {
		return Pipeline{}, ErrCheckRequired
	}
	if err = checkSelection(check.DataConnectionCheck, p.Tables); err != nil {
		return Pipeline{}, err
	}
	if err = s.q.PipelinePruneHistory(ctx, tx, sqlc.PipelinePruneHistoryParams{AccountID: p.AccountID, HistoryOffset: api.DataPipelineHistoryMax - api.DataPipelinesMax - 1, PageLimit: api.DataPipelineHistoryMax}); err != nil {
		return Pipeline{}, storeError(err)
	}
	count, err := s.q.PipelineCount(ctx, tx, p.AccountID)
	if err != nil {
		return Pipeline{}, storeError(err)
	}

	pending, err := s.q.PipelineSetupOtherReservations(ctx, tx, sqlc.PipelineSetupOtherReservationsParams{AccountID: p.AccountID, Name: p.Name, DatabaseID: p.DatabaseID, ConnectionID: p.SourceConnectionID})
	if err != nil {
		return Pipeline{}, storeError(err)
	}
	count += pending
	if count >= api.DataPipelinesMax {
		return Pipeline{}, &QuotaError{Limit: api.DataPipelinesMax, Observed: count}
	}
	tables, _ := json.Marshal(p.Tables)
	subjects, _ := json.Marshal(p.ReadSubjects)
	r, err := s.q.PipelineInsert(ctx, tx, sqlc.PipelineInsertParams{ID: p.ID, AccountID: p.AccountID, Name: p.Name, SourceConnectionID: p.SourceConnectionID, DatabaseID: p.DatabaseID, Tables: tables, ReadSubjects: subjects})
	if err != nil {
		return Pipeline{}, storeError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return Pipeline{}, storeError(err)
	}
	return pipelineRow(r), nil
}
func (s *PostgresStore) List(ctx context.Context, account string) ([]Pipeline, error) {
	rows, err := s.q.PipelinesList(ctx, s.pool, sqlc.PipelinesListParams{AccountID: account, PageLimit: api.DataPipelineHistoryMax})
	if err != nil {
		return nil, storeError(err)
	}
	items := make([]Pipeline, 0, len(rows))
	for _, r := range rows {
		items = append(items, pipelineRow(r))
	}
	return items, nil
}
func (s *PostgresStore) Get(ctx context.Context, account, id string) (Pipeline, error) {
	if !validID(account) || !validID(id) {
		return Pipeline{}, ErrNotFound
	}
	r, err := s.q.PipelineGet(ctx, s.pool, sqlc.PipelineGetParams{AccountID: account, ID: id})
	return pipelineRow(r), storeError(err)
}
func (s *PostgresStore) Control(ctx context.Context, account, id string, revision int64, action string) (Pipeline, error) {
	p, err := s.Get(ctx, account, id)
	if err != nil {
		return Pipeline{}, err
	}
	if revision <= 0 {
		return Pipeline{}, ErrInvalid
	}
	if p.Revision != revision {
		return Pipeline{}, ErrConflict
	}
	if p.DesiredState == "deleted" {
		if action == "delete" {
			return p, nil
		}
		return Pipeline{}, ErrConflict
	}
	arg := sqlc.PipelineControlParams{AccountID: account, ID: id, Revision: revision, DesiredState: p.DesiredState, VerifyRequested: p.VerifyRequested, State: p.State}
	switch action {
	case "pause":
		arg.DesiredState = "paused"
	case "resume":
		if p.State == "resync_required" {
			return Pipeline{}, ErrConflict
		}
		arg.DesiredState = "running"
	case "delete":
		arg.DesiredState = "deleted"
	case "resync":
		if p.State == "validating" || p.State == "copying" || p.State == "catching_up" || p.Generation >= api.DataPipelineGenerationsMax {
			return Pipeline{}, ErrConflict
		}
		arg.GenerationDelta = 1
		arg.VerifyRequested = false
		arg.DesiredState = "running"
		arg.State = "validating"
	case "verify":
		if p.State != "live" || p.VerifyRequested || tableWorkflow(p) || activeTableCancellation(p) {
			return Pipeline{}, ErrConflict
		}
		arg.VerifyRequested = true
	default:
		return Pipeline{}, ErrInvalid
	}
	r, err := s.q.PipelineControl(ctx, s.pool, arg)
	if errors.Is(err, pgx.ErrNoRows) {
		return Pipeline{}, ErrConflict
	}
	return pipelineRow(r), storeError(err)
}
func (s *PostgresStore) Claim(ctx context.Context, token string) (Pipeline, error) {
	r, err := s.q.PipelineClaim(ctx, s.pool, token)
	return pipelineRow(r), storeError(err)
}
func (s *PostgresStore) Finish(ctx context.Context, p Pipeline, ob Observation) error {
	if ob.ReaderOnly {
		return s.finishReaders(ctx, p, ob.ReaderChange)
	}
	if ob.Verification == "" {
		ob.Verification = "unknown"
	}
	arg := sqlc.PipelineFinishParams{AppliedThroughAt: pgtype.Timestamptz{Time: ob.AppliedThroughAt, Valid: !ob.AppliedThroughAt.IsZero()}, AccountID: p.AccountID, ID: p.ID, Revision: p.Revision, LeaseToken: p.LeaseToken, State: ob.State, Reason: ob.Reason, TablesReady: int32(ob.TablesReady), Verification: ob.Verification, Initialized: ob.Initialized, VerifiedAt: pgtype.Timestamptz{Time: ob.VerifiedAt, Valid: !ob.VerifiedAt.IsZero()}}
	if ob.ReaderChange != nil {
		arg.ReaderChange, _ = json.Marshal(ob.ReaderChange)
	}
	if ob.VerificationProgress != nil {
		arg.VerificationProgress, _ = json.Marshal(ob.VerificationProgress)
	}
	if ob.SchemaChange != nil {
		arg.SchemaChange, _ = json.Marshal(ob.SchemaChange)
		if activeTableCancellation(p) && ob.SchemaChange.Cancellation != nil && ob.SchemaChange.Cancellation.State == "complete" {
			arg.VerificationProgress = []byte("{}")
		}
	}
	if ob.RetainedWALBytes != nil {
		arg.RetainedWalBytes = pgtype.Int8{Int64: *ob.RetainedWALBytes, Valid: true}
	}
	n, err := s.q.PipelineFinish(ctx, s.pool, arg)
	if err != nil {
		return storeError(err)
	}
	if n == 0 {
		_ = s.q.PipelineRelease(ctx, s.pool, sqlc.PipelineReleaseParams{ID: p.ID, LeaseToken: p.LeaseToken})
		return ErrConflict
	}
	return nil
}
func (s *PostgresStore) TargetInUse(ctx context.Context, account, id string) (bool, error) {
	v, err := s.q.PipelineTargetInUse(ctx, s.pool, sqlc.PipelineTargetInUseParams{AccountID: account, DatabaseID: id})
	return v, storeError(err)
}
