package state

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/db"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	accesssql "github.com/onebox-faas/faas/pkg/state/orgaccess/sqlc"
)

// orgAccessPool leaves daemon work unchanged. A request with a fence takes
// a membership SHARE lock before its SQL and retains it through COMMIT.
// Removal/demotion takes an incompatible row lock, so both operations have a
// database-defined order even when they originate in different apid processes.
type orgAccessPool struct{ Pool *pgxpool.Pool }

func (p *orgAccessPool) Ping(ctx context.Context) error { return p.Pool.Ping(ctx) }

// Acquire is reserved for session-scoped LISTEN users; intent writes use
// the guarded transaction/query methods instead.
func (p *orgAccessPool) Acquire(ctx context.Context) (*pgxpool.Conn, error) {
	return db.DirectPool(p.Pool).Acquire(ctx)
}

func rollbackOrgAccess(ctx context.Context, tx pgx.Tx) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	_ = tx.Rollback(cleanup)
}

func (s *PgStore) CheckOrgAccess(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	return tx.Rollback(ctx)
}

func (s *PgStore) OrgAccessSnapshot(ctx context.Context, orgID, accountID string) (OrgAccessSnapshot, error) {
	row, err := accesssql.New().Snapshot(ctx, s.pool.Pool, accesssql.SnapshotParams{
		OrgID: mustPgUUID(orgID), AccountID: mustPgUUID(accountID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return OrgAccessSnapshot{}, ErrNotFound
	}
	return OrgAccessSnapshot{Role: OrgRole(row.Role), Revision: row.AccessRevision}, err
}

func (p *orgAccessPool) Begin(ctx context.Context) (pgx.Tx, error) {
	return p.BeginTx(ctx, pgx.TxOptions{})
}

func (p *orgAccessPool) BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	tx, err := p.Pool.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	if fence, ok := orgAccessFrom(ctx); ok {
		var checkErr error
		if options.AccessMode == pgx.ReadOnly {
			// A read-only snapshot cannot commit customer intent, and
			// PostgreSQL prohibits row locks in a read-only transaction.
			checkErr = checkOrgAccessSnapshot(ctx, tx, fence)
		} else {
			checkErr = lockOrgAccess(ctx, tx, fence)
		}
		if checkErr != nil {
			rollbackOrgAccess(ctx, tx)
			return nil, checkErr
		}
	}
	return tx, nil
}

// guardOrgAccessTx applies the request's access fence to a transaction begun
// on a session-locked connection, which bypasses orgAccessPool.BeginTx. It
// runs after the transaction's LOCK statements so their order is unchanged; a
// revocation committed after the snapshot surfaces as 40001 and is retried.
func guardOrgAccessTx(ctx context.Context, tx pgx.Tx) error {
	fence, ok := orgAccessFrom(ctx)
	if !ok {
		return nil
	}
	return lockOrgAccess(ctx, tx, fence)
}

func checkOrgAccessSnapshot(ctx context.Context, tx pgx.Tx, f OrgAccessFence) error {
	row, err := accesssql.New().Snapshot(ctx, tx, accesssql.SnapshotParams{OrgID: mustPgUUID(f.OrgID), AccountID: mustPgUUID(f.AccountID)})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && row.AccessRevision != f.Revision {
		return rejectOrgAccess(ctx)
	}
	return err
}

func lockOrgAccess(ctx context.Context, tx pgx.Tx, f OrgAccessFence) error {
	q := accesssql.New()
	row, err := q.LockMembership(ctx, tx, accesssql.LockMembershipParams{OrgID: mustPgUUID(f.OrgID), AccountID: mustPgUUID(f.AccountID)})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && row.AccessRevision != f.Revision {
		return rejectOrgAccess(ctx)
	}
	if err != nil || f.CredentialID == "" {
		return err
	}
	if f.AppID != "" {
		_, err = q.LockDeployToken(ctx, tx, accesssql.LockDeployTokenParams{ID: mustPgUUID(f.CredentialID), AccountID: mustPgUUID(f.AccountID), AppID: mustPgUUID(f.AppID)})
	} else if f.CredentialOrgID == "" {
		// Persisted API keys have a NOT NULL org binding. An unbound
		// synthetic principal is an account-scoped, short-lived OIDC bearer.
		_, err = q.LockOIDCBearer(ctx, tx, accesssql.LockOIDCBearerParams{ID: mustPgUUID(f.CredentialID), AccountID: mustPgUUID(f.AccountID)})
	} else {
		_, err = q.LockAPIKey(ctx, tx, accesssql.LockAPIKeyParams{ID: mustPgUUID(f.CredentialID), AccountID: mustPgUUID(f.AccountID), OrgID: mustPgUUID(f.CredentialOrgID)})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return rejectOrgAccess(ctx)
	}
	return err
}

func (p *orgAccessPool) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if _, ok := orgAccessFrom(ctx); !ok {
		return p.Pool.Exec(ctx, sql, args...)
	}
	tx, err := p.Begin(ctx)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	defer rollbackOrgAccess(ctx, tx)
	tag, err := tx.Exec(ctx, sql, args...)
	if err == nil {
		err = tx.Commit(ctx)
	}
	return tag, err
}

func (p *orgAccessPool) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if _, ok := orgAccessFrom(ctx); !ok {
		return p.Pool.QueryRow(ctx, sql, args...)
	}
	tx, err := p.Begin(ctx)
	if err != nil {
		return orgAccessRow{err: err}
	}
	return orgAccessRow{row: tx.QueryRow(ctx, sql, args...), tx: tx, ctx: ctx}
}

type orgAccessRow struct {
	row pgx.Row
	tx  pgx.Tx
	ctx context.Context
	err error
}

func (r orgAccessRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	defer rollbackOrgAccess(r.ctx, r.tx)
	if err := r.row.Scan(dest...); err != nil {
		return err
	}
	return r.tx.Commit(r.ctx)
}

func (p *orgAccessPool) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if _, ok := orgAccessFrom(ctx); !ok {
		return p.Pool.Query(ctx, sql, args...)
	}
	tx, err := p.Begin(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		rollbackOrgAccess(ctx, tx)
		return nil, err
	}
	return &orgAccessRows{Rows: rows, tx: tx, ctx: ctx}, nil
}

type orgAccessRows struct {
	pgx.Rows
	tx     pgx.Tx
	ctx    context.Context
	closed bool
	err    error
}

func (r *orgAccessRows) Close() {
	if r.closed {
		return
	}
	r.closed = true
	r.Rows.Close()
	if r.Rows.Err() != nil {
		r.err = r.Rows.Err()
		rollbackOrgAccess(r.ctx, r.tx)
		return
	}
	r.err = r.tx.Commit(r.ctx)
}

func (r *orgAccessRows) Next() bool {
	if r.Rows.Next() {
		return true
	}
	r.Close()
	return false
}

func (r *orgAccessRows) Err() error {
	if r.err != nil {
		return r.err
	}
	return r.Rows.Err()
}
