package copydatabases

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copydatabases/sqlc"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyroles"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

type Receipt struct {
	plan      Plan
	sourceOID uint32
	createdAt time.Time
}

func (Receipt) String() string     { return "private PostgreSQL database preparation receipt" }
func (r Receipt) GoString() string { return r.String() }
func (r Receipt) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct{ Prepared bool }{!r.createdAt.IsZero()})
}
func (r Receipt) CreatedAt() time.Time { return r.createdAt }

// These child SQL pins describe a closed database, not an imported dataset or
// admission/credential receipt. Provider placement still needs authentication.
func (r Receipt) TargetForWorker() (copyarchive.RestoreTarget, error) {
	if r.createdAt.IsZero() || r.plan.validateBody() != nil {
		return copyarchive.RestoreTarget{}, pgerrors.ErrConflict
	}
	d, _, err := r.plan.creationDatabase(r.sourceOID)
	if err != nil {
		return copyarchive.RestoreTarget{}, err
	}
	t := copyarchive.RestoreTarget(r.plan.body.Target)
	t.DatabaseName, t.DatabaseOID = d.Name, d.OID
	if _, err = t.Fingerprint(); err != nil {
		return copyarchive.RestoreTarget{}, err
	}
	return t, nil
}

// Prepare owns a dedicated bootstrap connection and prepares one entry from a
// complete immutable plan. The session lock spans top-level CREATE DATABASE and
// journal transactions. A creating claim can recover only its preallocated OID
// and exact initial metadata; a completed receipt never recreates a missing DB.
// The caller authenticates physical placement before and after borrowing SQL.
func Prepare(ctx context.Context, conn *pgx.Conn, source copyinventory.ExportPlan, plan Plan, sourceOID uint32, authorize copyroles.Authorize) (Receipt, error) {
	var zero Receipt
	if authorize == nil {
		return zero, pgerrors.ErrInvalid
	}
	if err := plan.validate(source); err != nil {
		return zero, err
	}
	d, existing, err := plan.creationDatabase(sourceOID)
	if err != nil {
		return zero, err
	}
	target := copyarchive.RestoreTarget(plan.body.Target)
	seed, err := copyroles.RecoverReceiptForWorker(source, target, plan.body.RoleProof)
	if err != nil {
		return zero, err
	}
	authorizeOnly := func() error { return authorization(ctx, target, authorize) }
	check := func() error {
		if err := authorizeOnly(); err != nil {
			return err
		}
		return seed.VerifyForWorker(ctx, conn)
	}
	if err = check(); err != nil {
		return zero, err
	}
	q := sqlc.New()
	// A canceled lock query can leave ownership uncertain. Closing the dedicated
	// connection releases any session lock PostgreSQL might have acquired.
	if err = q.LockCopyDatabases(ctx, conn); err != nil {
		closeConnection(ctx, conn)
		return zero, classify(ctx, err)
	}
	defer releaseLock(ctx, conn, q)
	if err = check(); err != nil {
		return zero, err
	}
	rows, err := ensureJournal(ctx, conn, q, plan, check, authorizeOnly)
	if err != nil {
		return zero, fmt.Errorf("retain database creation journal: %w", err)
	}
	present, err := verifyCatalogue(ctx, conn, plan, rows, sourceOID)
	if err != nil {
		return zero, err
	}
	row := receiptRow(rows, sourceOID)
	if existing || row.State == "created" {
		if !present {
			return zero, pgerrors.ErrConflict
		}
		if err = check(); err != nil {
			return zero, err
		}
		return Receipt{plan, sourceOID, row.CreatedAt.Time}, nil
	}
	if row.State == "reserved" {
		if present {
			return zero, pgerrors.ErrConflict
		}
		if err = transition(ctx, conn, q, sourceOID, false, check, authorizeOnly); err != nil {
			return zero, err
		}
	}
	if err = check(); err != nil {
		return zero, err
	}
	// Re-read after the durable claim. Prior-backend session lock release and a
	// missing database permit the original claim to retry with the same OID.
	rows, err = readJournal(ctx, conn, q, plan)
	if err != nil {
		return zero, err
	}
	present, err = verifyCatalogue(ctx, conn, plan, rows, sourceOID)
	if err != nil {
		return zero, err
	}
	if !present {
		if err = createDatabase(ctx, conn, q, plan, d, check); err != nil {
			return zero, fmt.Errorf("create closed target database: %w", err)
		}
	}
	if err = check(); err != nil {
		return zero, err
	}
	present, err = verifyCatalogue(ctx, conn, plan, rows, sourceOID)
	if err != nil {
		return zero, err
	}
	if !present {
		return zero, pgerrors.ErrConflict
	}
	if err = transition(ctx, conn, q, sourceOID, true, check, authorizeOnly); err != nil {
		return zero, err
	}
	if err = check(); err != nil {
		return zero, err
	}
	rows, err = readJournal(ctx, conn, q, plan)
	if err != nil {
		return zero, err
	}
	if _, err = verifyCatalogue(ctx, conn, plan, rows, sourceOID); err != nil {
		return zero, err
	}
	row = receiptRow(rows, sourceOID)
	if err = check(); err != nil {
		return zero, err
	}
	return Receipt{plan, sourceOID, row.CreatedAt.Time}, nil
}

func createDatabase(ctx context.Context, conn *pgx.Conn, q *sqlc.Queries, plan Plan, d copyinventory.Database, check func() error) error {
	var space string
	for _, s := range plan.body.Baseline.Tablespaces {
		if s.OID == d.TablespaceOID {
			space = s.Name
		}
	}
	command, err := q.FormatCopyDatabaseCreate(ctx, conn, sqlc.FormatCopyDatabaseCreateParams{
		DatabaseName: d.Name, OwnerName: d.Owner, Encoding: d.Encoding, CollationName: d.Collation, Ctype: d.CType,
		LocaleProvider: *d.LocaleProvider, Locale: nullableText(d.Locale), IcuRules: nullableText(d.ICURules), CollationVersion: nullableText(d.CollationVersion),
		Tablespace: space, TablespaceOid: oid(d.TablespaceOID), ConnectionLimit: d.ConnectionLimit, DatabaseOid: int64(d.OID),
	})
	if err != nil {
		return classify(ctx, err)
	}
	if err = check(); err != nil {
		return err
	}
	// Utility DDL has no identifier parameter binding and cannot run in a SQL
	// function/transaction. Only transmit the whole SQLC server-formatted command;
	// Go does not interpolate, concatenate or log SQL/configuration values.
	_, err = conn.Exec(ctx, command)
	if err != nil {
		return classify(ctx, err)
	}
	return nil
}
func nullableText(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}
func oid(n uint32) pgtype.Uint32 { return pgtype.Uint32{Uint32: n, Valid: true} }
func rollback(ctx context.Context, tx pgx.Tx) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_ = tx.Rollback(cleanup)
}
func closeConnection(ctx context.Context, conn *pgx.Conn) {
	if conn == nil {
		return
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_ = conn.Close(cleanup)
}
func releaseLock(ctx context.Context, conn *pgx.Conn, q *sqlc.Queries) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	ok, err := q.UnlockCopyDatabases(cleanup, conn)
	if err != nil || !ok {
		closeConnection(ctx, conn)
	}
}
func authorization(ctx context.Context, t copyarchive.RestoreTarget, authorize copyroles.Authorize) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := authorize(ctx, t)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		return nil
	}
	for _, kind := range []error{pgerrors.ErrInvalid, pgerrors.ErrConflict, pgerrors.ErrUnsupported, pgerrors.ErrNotFound, pgerrors.ErrQuotaExceeded, pgerrors.ErrUsageStale} {
		if errors.Is(err, kind) {
			return kind
		}
	}
	return pgerrors.ErrUnavailable
}
func classify(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return pgerrors.ErrConflict
	}
	var e *pgconn.PgError
	if errors.As(err, &e) {
		switch e.Code {
		case "55000", "42710", "42P04", "23505":
			return pgerrors.ErrConflict
		case "42501", "0A000":
			return pgerrors.ErrUnsupported
		case "22023":
			return pgerrors.ErrInvalid
		}
	}
	return pgerrors.ErrUnavailable
}
