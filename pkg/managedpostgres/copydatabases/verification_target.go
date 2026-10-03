package copydatabases

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copydatabases/sqlc"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

// VerificationTarget is an opaque, short-lived access capability. Retaining it
// after the callback cannot authorize another read window. Provider borrowing
// still requires fresh isolation checks around the complete child connection.
type VerificationTarget struct {
	window   *verification
	target   copyarchive.RestoreTarget
	openedAt time.Time
}

func (VerificationTarget) String() string                 { return "private PostgreSQL verification target" }
func (t VerificationTarget) GoString() string             { return t.String() }
func (t VerificationTarget) MarshalJSON() ([]byte, error) { return json.Marshal(struct{}{}) }
func (t VerificationTarget) TargetForWorker() (copyarchive.RestoreTarget, error) {
	if t.window == nil || t.openedAt.IsZero() || t.target.Validate() != nil {
		return copyarchive.RestoreTarget{}, pgerrors.ErrInvalid
	}
	return t.target, nil
}
func (t VerificationTarget) check(ctx context.Context) error {
	if _, err := t.TargetForWorker(); err != nil {
		return err
	}
	v := t.window
	rows, windows, _, err := v.read(ctx)
	if err != nil {
		return err
	}
	w := verificationWindow(windows, v.receipt.sourceOID)
	if !w.SourceOid.Valid || w.State != "open" || w.OwnerID.Bytes != v.owner || !w.OpenedAt.Time.Equal(t.openedAt) {
		return pgerrors.ErrConflict
	}
	if err = v.check(ctx); err != nil {
		return err
	}
	return v.verify(ctx, rows, windows)
}

// WithReadOnly runs inspection in an authenticated REPEATABLE READ, READ ONLY
// transaction and rolls it back before fresh provider and window checks. The
// callback is trusted platform code; PostgreSQL read-only semantics are not a
// privilege boundary against a worker that already owns the bootstrap role.
func (t VerificationTarget) WithReadOnly(ctx context.Context, conn *pgx.Conn, placement copyarchive.RestorePlacement, run func(context.Context, pgx.Tx) error) (err error) {
	if placement == nil || run == nil {
		return pgerrors.ErrInvalid
	}
	if err = t.check(ctx); err != nil {
		return err
	}
	if err = t.authenticate(ctx, conn, nil, false); err != nil {
		return err
	}
	if err = t.placement(ctx, conn, placement); err != nil {
		return err
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return classify(ctx, err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.PostgresCopyMaintenanceCleanupTimeout)
		defer cancel()
		if e := tx.Rollback(cleanup); e != nil {
			closeConnection(cleanup, conn)
			err = errors.Join(err, pgerrors.ErrUnavailable)
			return
		}
		if e := t.authenticate(cleanup, conn, nil, false); e != nil {
			err = errors.Join(err, e)
		}
		if e := t.placement(cleanup, conn, placement); e != nil {
			err = errors.Join(err, e)
		}
		if err == nil {
			err = t.check(ctx)
		}
	}()
	if err = t.authenticate(ctx, conn, tx, true); err != nil {
		return err
	}
	if err = t.check(ctx); err != nil {
		return err
	}
	if err = run(ctx, tx); err != nil {
		return maintenanceCallbackError(ctx, err)
	}
	return t.authenticate(ctx, conn, tx, true)
}
func (t VerificationTarget) authenticate(ctx context.Context, conn *pgx.Conn, tx pgx.Tx, readOnly bool) error {
	a := t.target
	if conn == nil || conn.IsClosed() || conn.PgConn().IsBusy() || conn.Config().User != a.RoleName || conn.Config().Database != a.DatabaseName {
		return pgerrors.ErrConflict
	}
	status := byte('I')
	var db sqlc.DBTX = conn
	if tx != nil {
		status = 'T'
		db = tx
	}
	if conn.PgConn().TxStatus() != status {
		return pgerrors.ErrConflict
	}
	identity, err := t.window.q.CopyDatabaseMaintenanceBootstrapIdentity(ctx, db)
	if err != nil {
		return classify(ctx, err)
	}
	if int(identity.ServerVersion/10000) != a.Scope.PostgresMajor || identity.DatabaseName != a.DatabaseName || !identity.DatabaseOid.Valid || identity.DatabaseOid.Uint32 != a.DatabaseOID || identity.RoleName != a.RoleName || identity.SessionRole != a.RoleName || !identity.RoleOid.Valid || identity.RoleOid.Uint32 != a.RoleOID || identity.ReadOnly != readOnly {
		return pgerrors.ErrConflict
	}
	return ctx.Err()
}
func (t VerificationTarget) placement(ctx context.Context, conn *pgx.Conn, placement copyarchive.RestorePlacement) error {
	return maintenanceCallbackError(ctx, placement(ctx, conn, t.target))
}
