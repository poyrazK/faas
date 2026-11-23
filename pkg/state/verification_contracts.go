package state

// ADR-20261122220000: verification contracts. An app's gregale.verify.yaml is
// stored in its validated JSON form; imaged runs it against each candidate
// before promotion. Read-only SQL checks are handed to apid through
// deployment_verification_queries because only apid holds managed Postgres
// credentials.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

// VerificationContract is one app's stored contract.
type VerificationContract struct {
	AppID      string
	AccountID  string
	Contract   json.RawMessage
	SHA256     string
	CheckCount int
	Revision   int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ErrVerificationContractRevision means the caller's expected revision is
// not the stored one.
var ErrVerificationContractRevision = fmt.Errorf("%w: verification contract changed", ErrConflict)

// VerificationContractStore reads and writes app contracts.
type VerificationContractStore interface {
	// PutAppVerificationContract replaces the app's contract. A non-nil
	// expectedRevision must match the stored revision (0: no contract).
	PutAppVerificationContract(ctx context.Context, c VerificationContract, expectedRevision *int64) (VerificationContract, error)
	// AppVerificationContract returns ErrNotFound when the app has none.
	AppVerificationContract(ctx context.Context, appID string) (VerificationContract, error)
	// DeleteAppVerificationContract removes the contract; a non-nil
	// expectedRevision must match the stored revision.
	DeleteAppVerificationContract(ctx context.Context, appID, accountID string, expectedRevision *int64) error
}

// Verification query states.
const (
	VerificationQueryPending = "pending"
	VerificationQueryRunning = "running"
	VerificationQueryDone    = "done"
)

// VerificationQuery is one read-only SQL check handed from imaged to apid.
// The result keeps only the row count and the first value.
type VerificationQuery struct {
	ID           string
	DeploymentID string
	AppID        string
	AccountID    string
	CheckName    string
	Database     string
	Query        string
	Status       string
	RowCount     int
	HasFirst     bool
	FirstValue   *string
	ErrorCode    string
	Error        string
	CreatedAt    time.Time
	ExpiresAt    time.Time
	claimedAt    time.Time // MemStore only
}

// VerificationQueryStore is the SQL check handoff.
type VerificationQueryStore interface {
	CreateVerificationQuery(ctx context.Context, q VerificationQuery) (VerificationQuery, error)
	VerificationQueryByID(ctx context.Context, id string) (VerificationQuery, error)
	// ClaimVerificationQueries moves up to limit pending, unexpired queries
	// to running and returns them, and takes back running queries whose
	// claim is older than api.VerificationQueryStaleClaim (their apid
	// stopped). Concurrent claimers never share a row.
	ClaimVerificationQueries(ctx context.Context, now time.Time, limit int) ([]VerificationQuery, error)
	// CompleteVerificationQuery records a running query's outcome.
	CompleteVerificationQuery(ctx context.Context, q VerificationQuery) error
	// DeleteExpiredVerificationQueries removes rows that expired before.
	DeleteExpiredVerificationQueries(ctx context.Context, before time.Time) (int64, error)
}

// --- PostgreSQL ---

const verificationContractCols = `app_id, account_id, contract, contract_sha256, check_count, revision, created_at, updated_at`

func scanVerificationContract(scan func(...any) error) (VerificationContract, error) {
	var c VerificationContract
	var contract []byte
	if err := scan(&c.AppID, &c.AccountID, &contract, &c.SHA256, &c.CheckCount, &c.Revision, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return VerificationContract{}, err
	}
	c.Contract = contract
	return c, nil
}

func (s *PgStore) PutAppVerificationContract(ctx context.Context, c VerificationContract, expectedRevision *int64) (VerificationContract, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return VerificationContract{}, fmt.Errorf("state: put verification contract: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var owner string
	if err := tx.QueryRow(ctx, `select account_id::text from apps where id = $1 for update`, c.AppID).Scan(&owner); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return VerificationContract{}, ErrNotFound
		}
		return VerificationContract{}, fmt.Errorf("state: put verification contract: %w", err)
	}
	if owner != c.AccountID {
		return VerificationContract{}, ErrNotFound
	}
	var current int64
	err = tx.QueryRow(ctx, `select revision from app_verification_contracts where app_id = $1`, c.AppID).Scan(&current)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return VerificationContract{}, fmt.Errorf("state: put verification contract: %w", err)
	}
	if expectedRevision != nil && *expectedRevision != current {
		return VerificationContract{}, ErrVerificationContractRevision
	}
	row := tx.QueryRow(ctx, `insert into app_verification_contracts
		(app_id, account_id, contract, contract_sha256, byte_size, check_count, revision)
		values ($1, $2, $3::jsonb, $4, $5, $6, 1)
		on conflict (app_id) do update set contract = excluded.contract,
			contract_sha256 = excluded.contract_sha256, byte_size = excluded.byte_size,
			check_count = excluded.check_count,
			revision = app_verification_contracts.revision + 1, updated_at = now()
		returning `+verificationContractCols,
		c.AppID, c.AccountID, string(c.Contract), c.SHA256, len(c.Contract), c.CheckCount)
	stored, err := scanVerificationContract(row.Scan)
	if err != nil {
		return VerificationContract{}, fmt.Errorf("state: put verification contract: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return VerificationContract{}, fmt.Errorf("state: put verification contract: %w", err)
	}
	return stored, nil
}

func (s *PgStore) AppVerificationContract(ctx context.Context, appID string) (VerificationContract, error) {
	row := s.pool.QueryRow(ctx, `select `+verificationContractCols+` from app_verification_contracts where app_id = $1`, appID)
	c, err := scanVerificationContract(row.Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return VerificationContract{}, ErrNotFound
	}
	if err != nil {
		return VerificationContract{}, fmt.Errorf("state: get verification contract: %w", err)
	}
	return c, nil
}

func (s *PgStore) DeleteAppVerificationContract(ctx context.Context, appID, accountID string, expectedRevision *int64) error {
	tag, err := s.pool.Exec(ctx, `delete from app_verification_contracts
		where app_id = $1 and account_id = $2 and ($3::bigint is null or revision = $3)`, appID, accountID, expectedRevision)
	if err != nil {
		return fmt.Errorf("state: delete verification contract: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	if expectedRevision != nil {
		var exists bool
		if err := s.pool.QueryRow(ctx, `select exists(select 1 from app_verification_contracts where app_id = $1 and account_id = $2)`, appID, accountID).Scan(&exists); err != nil {
			return fmt.Errorf("state: delete verification contract: %w", err)
		}
		if exists {
			return ErrVerificationContractRevision
		}
	}
	return ErrNotFound
}

const verificationQueryCols = `id, deployment_id, app_id, account_id, check_name, database_name, query, status,
	coalesce(row_count, 0), coalesce(has_first, false), first_value, coalesce(error_code, ''), coalesce(error, ''), created_at, expires_at`

func scanVerificationQuery(scan func(...any) error) (VerificationQuery, error) {
	var q VerificationQuery
	err := scan(&q.ID, &q.DeploymentID, &q.AppID, &q.AccountID, &q.CheckName, &q.Database, &q.Query, &q.Status,
		&q.RowCount, &q.HasFirst, &q.FirstValue, &q.ErrorCode, &q.Error, &q.CreatedAt, &q.ExpiresAt)
	return q, err
}

func (s *PgStore) CreateVerificationQuery(ctx context.Context, q VerificationQuery) (VerificationQuery, error) {
	row := s.pool.QueryRow(ctx, `insert into deployment_verification_queries
		(deployment_id, app_id, account_id, check_name, database_name, query, expires_at)
		values ($1, $2, $3, $4, $5, $6, $7) returning `+verificationQueryCols,
		q.DeploymentID, q.AppID, q.AccountID, q.CheckName, q.Database, q.Query, q.ExpiresAt)
	created, err := scanVerificationQuery(row.Scan)
	if err != nil {
		return VerificationQuery{}, fmt.Errorf("state: create verification query: %w", err)
	}
	return created, nil
}

func (s *PgStore) VerificationQueryByID(ctx context.Context, id string) (VerificationQuery, error) {
	if _, err := uuid.Parse(id); err != nil {
		return VerificationQuery{}, ErrNotFound
	}
	q, err := scanVerificationQuery(s.pool.QueryRow(ctx, `select `+verificationQueryCols+` from deployment_verification_queries where id = $1`, id).Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return VerificationQuery{}, ErrNotFound
	}
	if err != nil {
		return VerificationQuery{}, fmt.Errorf("state: get verification query: %w", err)
	}
	return q, nil
}

func (s *PgStore) ClaimVerificationQueries(ctx context.Context, now time.Time, limit int) ([]VerificationQuery, error) {
	rows, err := s.pool.Query(ctx, `update deployment_verification_queries set status = 'running', claimed_at = $1
		where id in (select id from deployment_verification_queries
			where (status = 'pending' or (status = 'running' and claimed_at < $3)) and expires_at > $1
			order by created_at limit $2 for update skip locked)
		returning `+verificationQueryCols, now, limit, now.Add(-api.VerificationQueryStaleClaim))
	if err != nil {
		return nil, fmt.Errorf("state: claim verification queries: %w", err)
	}
	defer rows.Close()
	var out []VerificationQuery
	for rows.Next() {
		q, err := scanVerificationQuery(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("state: claim verification queries: %w", err)
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

func (s *PgStore) CompleteVerificationQuery(ctx context.Context, q VerificationQuery) error {
	tag, err := s.pool.Exec(ctx, `update deployment_verification_queries set status = 'done', completed_at = now(),
		row_count = $2, has_first = $3, first_value = $4, error_code = nullif($5, ''), error = nullif($6, '')
		where id = $1 and status = 'running'`,
		q.ID, q.RowCount, q.HasFirst, q.FirstValue, q.ErrorCode, q.Error)
	if err != nil {
		return fmt.Errorf("state: complete verification query: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) DeleteExpiredVerificationQueries(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `delete from deployment_verification_queries where expires_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("state: delete expired verification queries: %w", err)
	}
	return tag.RowsAffected(), nil
}

// --- Memory ---

func (m *MemStore) PutAppVerificationContract(_ context.Context, c VerificationContract, expectedRevision *int64) (VerificationContract, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[c.AppID]
	if !ok || app.AccountID != c.AccountID {
		return VerificationContract{}, ErrNotFound
	}
	if m.verificationContracts == nil {
		m.verificationContracts = map[string]VerificationContract{}
	}
	current, exists := m.verificationContracts[c.AppID]
	if expectedRevision != nil && *expectedRevision != current.Revision {
		return VerificationContract{}, ErrVerificationContractRevision
	}
	now := time.Now().UTC()
	c.Contract = append(json.RawMessage(nil), c.Contract...)
	c.Revision = current.Revision + 1
	c.CreatedAt, c.UpdatedAt = now, now
	if exists {
		c.CreatedAt = current.CreatedAt
	}
	m.verificationContracts[c.AppID] = c
	return c, nil
}

func (m *MemStore) AppVerificationContract(_ context.Context, appID string) (VerificationContract, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.verificationContracts[appID]
	if !ok {
		return VerificationContract{}, ErrNotFound
	}
	return c, nil
}

func (m *MemStore) DeleteAppVerificationContract(_ context.Context, appID, accountID string, expectedRevision *int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.verificationContracts[appID]
	if !ok || c.AccountID != accountID {
		return ErrNotFound
	}
	if expectedRevision != nil && *expectedRevision != c.Revision {
		return ErrVerificationContractRevision
	}
	delete(m.verificationContracts, appID)
	return nil
}

func (m *MemStore) CreateVerificationQuery(_ context.Context, q VerificationQuery) (VerificationQuery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.verificationQueries == nil {
		m.verificationQueries = map[string]VerificationQuery{}
	}
	q.ID = uuid.NewString()
	q.Status = VerificationQueryPending
	q.CreatedAt = time.Now().UTC()
	m.verificationQueries[q.ID] = q
	return q, nil
}

func (m *MemStore) VerificationQueryByID(_ context.Context, id string) (VerificationQuery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	q, ok := m.verificationQueries[id]
	if !ok {
		return VerificationQuery{}, ErrNotFound
	}
	return q, nil
}

func (m *MemStore) ClaimVerificationQueries(_ context.Context, now time.Time, limit int) ([]VerificationQuery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []VerificationQuery
	for id, q := range m.verificationQueries {
		if len(out) >= limit {
			break
		}
		stale := q.Status == VerificationQueryRunning && q.claimedAt.Before(now.Add(-api.VerificationQueryStaleClaim))
		if (q.Status == VerificationQueryPending || stale) && q.ExpiresAt.After(now) {
			q.Status, q.claimedAt = VerificationQueryRunning, now
			m.verificationQueries[id] = q
			out = append(out, q)
		}
	}
	return out, nil
}

func (m *MemStore) CompleteVerificationQuery(_ context.Context, q VerificationQuery) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.verificationQueries[q.ID]
	if !ok || current.Status != VerificationQueryRunning {
		return ErrNotFound
	}
	current.Status = VerificationQueryDone
	current.RowCount, current.HasFirst, current.FirstValue = q.RowCount, q.HasFirst, q.FirstValue
	current.ErrorCode, current.Error = q.ErrorCode, q.Error
	m.verificationQueries[q.ID] = current
	return nil
}

func (m *MemStore) DeleteExpiredVerificationQueries(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for id, q := range m.verificationQueries {
		if q.ExpiresAt.Before(before) {
			delete(m.verificationQueries, id)
			n++
		}
	}
	return n, nil
}

var (
	_ VerificationContractStore = (*PgStore)(nil)
	_ VerificationContractStore = (*MemStore)(nil)
	_ VerificationQueryStore    = (*PgStore)(nil)
	_ VerificationQueryStore    = (*MemStore)(nil)
)
