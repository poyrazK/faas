package copydatabases

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copydatabases/sqlc"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyroles"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

// MaintenanceClosure attests only to restoration of original closed admission
// and configuration. Callback execution, import equivalence and readiness need
// separate durable evidence. The first SQL completion time survives handoff.
type MaintenanceClosure struct {
	planFingerprint                          string
	sourceOID, targetOID                     uint32
	ownerID                                  uuid.UUID
	preparationCreatedAt, openedAt, closedAt time.Time
}

func (MaintenanceClosure) String() string     { return "private PostgreSQL maintenance closure" }
func (c MaintenanceClosure) GoString() string { return c.String() }
func (c MaintenanceClosure) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct{ Closed bool }{!c.closedAt.IsZero()})
}
func (c MaintenanceClosure) ClosedAt() time.Time { return c.closedAt }

// MaintenanceRun must synchronously borrow the supplied child target, respect
// ctx, and close all child connections before returning. Provider placement and
// durable import authority remain the caller's responsibility. It must reserve
// the original dispatch owner before any SQL; this window cannot mint one.
type MaintenanceRun func(context.Context, copyarchive.RestoreTarget) error

// WithMaintenance holds the original bootstrap session lock across admission,
// the callback, and bounded closure. Only a never-opened original dispatch runs
// the callback. Any existing window is recovered close-only and returns conflict.
// New customer roles remain NOLOGIN; other non-admin logins must have neither
// CONNECT nor owner membership. Database ACLs, including NULL, stay untouched.
func (r Receipt) WithMaintenance(ctx context.Context, conn *pgx.Conn, source copyinventory.ExportPlan, dispatch uuid.UUID, authorize copyroles.Authorize, run MaintenanceRun) (result MaintenanceClosure, err error) {
	if run == nil {
		return result, pgerrors.ErrInvalid
	}
	m, err := newMaintenance(r, conn, source, dispatch, authorize)
	if err != nil {
		return result, err
	}
	if err = m.lock(ctx); err != nil {
		return result, err
	}
	defer releaseLock(ctx, conn, m.q)
	rows, windows, err := m.read(ctx)
	if err != nil {
		return result, err
	}
	if w := maintenanceWindow(windows, r.sourceOID); w.SourceOid.Valid {
		if w.OwnerID.Bytes != dispatch {
			return result, pgerrors.ErrConflict
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.PostgresCopyMaintenanceCleanupTimeout)
		defer cancel()
		_, closeErr := m.closeOwned(cleanup, false)
		return result, errors.Join(pgerrors.ErrConflict, closeErr)
	}
	// A crashed window on another database retains sole maintenance ownership
	// until its original worker closes it. Never open a second private window.
	for _, w := range windows {
		if w.State != "closed" {
			return result, pgerrors.ErrConflict
		}
	}
	if err = m.check(ctx); err != nil {
		return result, err
	}
	if err = m.verify(ctx, rows, windows); err != nil {
		return result, err
	}
	// A failed/unknown opening transaction can still have committed. Always read
	// its original row and close if present, even when the dispatch lease expired.
	absentOK := true
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.PostgresCopyMaintenanceCleanupTimeout)
		defer cancel()
		closed, closeErr := m.closeOwned(cleanup, absentOK)
		if closeErr != nil {
			result = MaintenanceClosure{}
			err = errors.Join(err, fmt.Errorf("close database maintenance: %w", closeErr))
			return
		}
		if err == nil {
			if err = m.check(ctx); err == nil {
				result = closed
			}
		}
	}()
	if err = m.open(ctx); err != nil {
		return result, err
	}
	absentOK = false
	rows, windows, err = m.read(ctx)
	if err != nil {
		return result, err
	}
	if err = m.verify(ctx, rows, windows); err != nil {
		return result, err
	}
	if err = m.check(ctx); err != nil {
		return result, err
	}
	child, _ := r.TargetForWorker()
	if err = run(ctx, child); err != nil {
		err = maintenanceCallbackError(ctx, err)
		return result, err
	}
	if err = m.check(ctx); err != nil {
		return result, err
	}
	private, e := m.q.CopyDatabaseMaintenanceSessionsPrivate(ctx, conn, sqlc.CopyDatabaseMaintenanceSessionsPrivateParams{Column1: oid(m.database.OID), Column2: int32(api.PostgresCopyMaintenanceConnections)})
	if e != nil {
		err = classify(ctx, e)
		return result, err
	}
	if !private.Valid || !private.Bool {
		err = pgerrors.ErrConflict
		return result, err
	}
	rows, windows, err = m.read(ctx)
	if err == nil {
		err = m.verify(ctx, rows, windows)
	}
	return result, err
}

// CloseMaintenance is a handoff/recovery operation. It never opens admission or
// invokes import SQL. Fresh close-only ownership authorizes the borrower/lock;
// exact retained window ownership permits quiescing before catalogue validation.
// Missing/damaged journals are not installed or repaired. A terminal retry
// returns the original timestamp only after full original catalogue verification.
func (r Receipt) CloseMaintenance(ctx context.Context, conn *pgx.Conn, source copyinventory.ExportPlan, dispatch uuid.UUID, authorize copyroles.Authorize) (MaintenanceClosure, error) {
	m, err := newMaintenance(r, conn, source, dispatch, authorize)
	if err != nil {
		return MaintenanceClosure{}, err
	}
	if err = m.lock(ctx); err != nil {
		return MaintenanceClosure{}, err
	}
	defer releaseLock(ctx, conn, m.q)
	c, err := m.closeOwned(ctx, false)
	if err == nil {
		err = authorization(ctx, m.target, authorize)
	}
	if err != nil {
		return MaintenanceClosure{}, err
	}
	return c, nil
}

type maintenance struct {
	receipt     Receipt
	database    copyinventory.Database
	conn        *pgx.Conn
	q           *sqlc.Queries
	target      copyarchive.RestoreTarget
	seed        copyroles.Receipt
	dispatch    uuid.UUID
	fingerprint string
	authorize   copyroles.Authorize
}

func newMaintenance(r Receipt, conn *pgx.Conn, source copyinventory.ExportPlan, dispatch uuid.UUID, authorize copyroles.Authorize) (*maintenance, error) {
	if authorize == nil || dispatch == uuid.Nil {
		return nil, pgerrors.ErrInvalid
	}
	if !r.preparationValid() || r.plan.validate(source) != nil {
		return nil, pgerrors.ErrConflict
	}
	t := copyarchive.RestoreTarget(r.plan.body.Target)
	for _, id := range []string{t.OwnerID, t.Scope.OperationID, t.Scope.AccountID, t.Scope.ProjectID, t.Scope.SourceDatabaseID, t.Scope.CaptureDatabaseID} {
		if dispatch.String() == id {
			return nil, pgerrors.ErrInvalid
		}
	}
	d, _, err := r.plan.creationDatabase(r.sourceOID)
	if err != nil {
		return nil, err
	}
	// An open baseline or the journal's bootstrap database needs a separately
	// owned closure/relocation protocol. Every entry stays in the complete plan.
	if d.AllowConnections || d.OID == t.DatabaseOID || d.Name == t.DatabaseName {
		return nil, pgerrors.ErrUnsupported
	}
	seed, err := copyroles.RecoverReceiptForWorker(source, t, r.plan.body.RoleProof)
	if err != nil {
		return nil, err
	}
	fp, err := preparationPlanFingerprint(r.plan)
	if err != nil {
		return nil, err
	}
	return &maintenance{r, d, conn, sqlc.New(), t, seed, dispatch, fp, authorize}, nil
}
func (m *maintenance) authenticate(ctx context.Context) error {
	c, t := m.conn, m.target
	if c == nil || c.IsClosed() || c.PgConn().IsBusy() || c.PgConn().TxStatus() != 'I' || c.Config().User != t.RoleName || c.Config().Database != t.DatabaseName {
		return pgerrors.ErrConflict
	}
	a, err := m.q.CopyDatabaseMaintenanceBootstrapIdentity(ctx, c)
	if err != nil {
		return classify(ctx, err)
	}
	if int(a.ServerVersion/10000) != t.Scope.PostgresMajor || a.DatabaseName != t.DatabaseName || !a.DatabaseOid.Valid || a.DatabaseOid.Uint32 != t.DatabaseOID || a.RoleName != t.RoleName || a.SessionRole != t.RoleName || !a.RoleOid.Valid || a.RoleOid.Uint32 != t.RoleOID || a.ReadOnly {
		return pgerrors.ErrConflict
	}
	return ctx.Err()
}
func (m *maintenance) lock(ctx context.Context) error {
	if err := m.authenticate(ctx); err != nil {
		return err
	}
	if err := authorization(ctx, m.target, m.authorize); err != nil {
		return err
	}
	if err := m.q.LockCopyDatabases(ctx, m.conn); err != nil {
		closeConnection(ctx, m.conn)
		return classify(ctx, err)
	}
	// The caller registers unlock only on success.
	if err := m.authenticate(ctx); err != nil {
		releaseLock(ctx, m.conn, m.q)
		return err
	}
	if err := authorization(ctx, m.target, m.authorize); err != nil {
		releaseLock(ctx, m.conn, m.q)
		return err
	}
	return nil
}
func (m *maintenance) check(ctx context.Context) error {
	if err := authorization(ctx, m.target, m.authorize); err != nil {
		return err
	}
	return m.seed.VerifyForWorker(ctx, m.conn)
}
func (m *maintenance) read(ctx context.Context) ([]sqlc.GregaleCopyDatabasesDatabase, []sqlc.GregaleCopyDatabaseMaintenanceWindow, error) {
	rows, windows, err := m.readBase(ctx)
	if err != nil {
		return nil, nil, err
	}
	verification, err := readVerificationJournal(ctx, m, rows, windows)
	if err != nil {
		return nil, nil, err
	}
	for _, w := range verification {
		if w.State != "closed" {
			return nil, nil, pgerrors.ErrConflict
		}
	}
	return rows, windows, nil
}

// readBase validates the immutable preparation and import journals. Verification
// reads use it directly so a close-only recovery can observe its own open window.
func (m *maintenance) readBase(ctx context.Context) ([]sqlc.GregaleCopyDatabasesDatabase, []sqlc.GregaleCopyDatabaseMaintenanceWindow, error) {
	if err := m.authenticate(ctx); err != nil {
		return nil, nil, err
	}
	present, err := m.q.CopyDatabaseSchemaExists(ctx, m.conn)
	if err != nil {
		return nil, nil, classify(ctx, err)
	}
	if !present {
		return nil, nil, pgerrors.ErrConflict
	}
	rows, err := readJournal(ctx, m.conn, m.q, m.receipt.plan)
	if err != nil {
		return nil, nil, err
	}
	row := receiptRow(rows, m.receipt.sourceOID)
	if (row.State != "created" && row.State != "existing") || !row.CreatedAt.Valid || !row.CreatedAt.Time.Equal(m.receipt.createdAt) {
		return nil, nil, pgerrors.ErrConflict
	}
	windows, err := m.readMaintenanceJournal(ctx, rows)
	return rows, windows, err
}
func (m *maintenance) readMaintenanceJournal(ctx context.Context, rows []sqlc.GregaleCopyDatabasesDatabase) ([]sqlc.GregaleCopyDatabaseMaintenanceWindow, error) {
	present, err := m.q.CopyDatabaseMaintenanceSchemaExists(ctx, m.conn)
	if err != nil {
		return nil, classify(ctx, err)
	}
	if !present {
		return nil, nil
	}
	private, err := m.q.PrivateCopyDatabaseMaintenanceJournal(ctx, m.conn)
	if err != nil {
		return nil, classify(ctx, err)
	}
	if !private {
		return nil, pgerrors.ErrConflict
	}
	windows, err := m.q.CopyDatabaseMaintenanceWindows(ctx, m.conn)
	if err != nil {
		return nil, classify(ctx, err)
	}
	seen, owners, targets := map[uint32]bool{}, map[uuid.UUID]bool{}, map[uint32]bool{}
	active := 0
	for _, w := range windows {
		d, _, e := m.receipt.plan.creationDatabase(w.SourceOid.Uint32)
		made := receiptRow(rows, w.SourceOid.Uint32)
		if e != nil || !w.SourceOid.Valid || !w.TargetOid.Valid || !w.OwnerID.Valid || w.OwnerID.Bytes == uuid.Nil || seen[w.SourceOid.Uint32] || owners[w.OwnerID.Bytes] || targets[w.TargetOid.Uint32] || w.TargetOid.Uint32 != d.OID || d.AllowConnections || d.OID == m.target.DatabaseOID || w.PlanFingerprint != m.fingerprint || !made.CreatedAt.Valid || (made.State != "created" && made.State != "existing") || !finiteSQLTime(w.PreparationCreatedAt) || !w.PreparationCreatedAt.Time.Equal(made.CreatedAt.Time) || !finiteSQLTime(w.OpenedAt) || w.OpenedAt.Time.Before(w.PreparationCreatedAt.Time) {
			return nil, pgerrors.ErrConflict
		}
		seen[w.SourceOid.Uint32], owners[w.OwnerID.Bytes], targets[w.TargetOid.Uint32] = true, true, true
		switch w.State {
		case "open", "closing":
			active++
			if w.ClosedAt.Valid {
				return nil, pgerrors.ErrConflict
			}
		case "closed":
			if !finiteSQLTime(w.ClosedAt) || w.ClosedAt.Time.Before(w.OpenedAt.Time) {
				return nil, pgerrors.ErrConflict
			}
		default:
			return nil, pgerrors.ErrConflict
		}
	}
	if active > 1 {
		return nil, pgerrors.ErrConflict
	}
	return windows, nil
}
func finiteSQLTime(t pgtype.Timestamptz) bool {
	return t.Valid && t.InfinityModifier == 0 && !t.Time.IsZero() && t.Time.Year() >= 1 && t.Time.Year() <= 9999 && t.Time.Nanosecond()%1000 == 0 && !t.Time.After(time.Now())
}
func maintenanceWindow(rows []sqlc.GregaleCopyDatabaseMaintenanceWindow, id uint32) sqlc.GregaleCopyDatabaseMaintenanceWindow {
	for _, r := range rows {
		if r.SourceOid.Uint32 == id {
			return r
		}
	}
	return sqlc.GregaleCopyDatabaseMaintenanceWindow{}
}
func (m *maintenance) verify(ctx context.Context, rows []sqlc.GregaleCopyDatabasesDatabase, windows []sqlc.GregaleCopyDatabaseMaintenanceWindow) error {
	overrides := map[uint32]copyinventory.Database{}
	for _, w := range windows {
		if w.State == "closed" {
			continue
		}
		d, _, _ := m.receipt.plan.creationDatabase(w.SourceOid.Uint32)
		d.AllowConnections, d.Template, d.ConnectionLimit = w.State == "open", false, int32(api.PostgresCopyMaintenanceConnections)
		overrides[d.OID] = d
	}
	present, err := verifyCatalogueExpected(ctx, m.conn, m.receipt.plan, rows, m.receipt.sourceOID, overrides)
	if err != nil {
		return err
	}
	if !present {
		return pgerrors.ErrConflict
	}
	return nil
}
func (m *maintenance) params(action string) sqlc.ChangeCopyDatabaseMaintenanceAdmissionParams {
	d := m.database
	return sqlc.ChangeCopyDatabaseMaintenanceAdmissionParams{SourceOid: oid(m.receipt.sourceOID), TargetOid: oid(d.OID), DatabaseName: d.Name, OwnerOid: oid(d.OwnerOID), DispatchOwner: pgtype.UUID{Bytes: m.dispatch, Valid: true}, PlanFingerprint: m.fingerprint, PreparedAt: pgtype.Timestamptz{Time: m.receipt.createdAt, Valid: true}, Action: action, OriginalTemplate: d.Template, OriginalLimit: d.ConnectionLimit, MaintenanceLimit: int32(api.PostgresCopyMaintenanceConnections)}
}
func (m *maintenance) open(ctx context.Context) error {
	if err := m.check(ctx); err != nil {
		return err
	}
	tx, err := m.conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadWrite})
	if err != nil {
		return classify(ctx, err)
	}
	defer rollback(ctx, tx)
	present, err := m.q.CopyDatabaseMaintenanceSchemaExists(ctx, tx)
	if err != nil {
		return classify(ctx, err)
	}
	if !present {
		for _, install := range []func(context.Context, sqlc.DBTX) error{m.q.InstallCopyDatabaseMaintenanceSchema, m.q.InstallCopyDatabaseMaintenanceWindows, m.q.InstallCopyDatabaseMaintenanceActiveIndex} {
			if err = install(ctx, tx); err != nil {
				return classify(ctx, err)
			}
		}
	}
	private, err := m.q.PrivateCopyDatabaseMaintenanceJournal(ctx, tx)
	if err != nil {
		return classify(ctx, err)
	}
	if !private {
		return pgerrors.ErrConflict
	}
	a := m.params("open")
	if err = m.q.InsertCopyDatabaseMaintenanceWindow(ctx, tx, sqlc.InsertCopyDatabaseMaintenanceWindowParams{Column1: a.SourceOid, Column2: a.TargetOid, Column3: a.DispatchOwner, Column4: a.PlanFingerprint, Column5: a.PreparedAt}); err != nil {
		return classify(ctx, err)
	}
	if err = m.change(ctx, tx, "open"); err != nil {
		return err
	}
	if err = authorization(ctx, m.target, m.authorize); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return classify(ctx, err)
	}
	return nil
}
func (m *maintenance) change(ctx context.Context, tx pgx.Tx, action string) error {
	if err := m.q.InstallCopyDatabaseMaintenanceMutation(ctx, tx); err != nil {
		return classify(ctx, err)
	}
	ok, err := m.q.ChangeCopyDatabaseMaintenanceAdmission(ctx, tx, m.params(action))
	if err != nil {
		return classify(ctx, err)
	}
	if !ok {
		return pgerrors.ErrConflict
	}
	return nil
}
func (m *maintenance) changeCommitted(ctx context.Context, action string) error {
	tx, err := m.conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadWrite})
	if err != nil {
		return classify(ctx, err)
	}
	defer rollback(ctx, tx)
	if err = m.change(ctx, tx, action); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return classify(ctx, err)
	}
	return nil
}
func (m *maintenance) closeOwned(ctx context.Context, absentOK bool) (MaintenanceClosure, error) {
	rows, windows, err := m.read(ctx)
	if err != nil {
		return MaintenanceClosure{}, err
	}
	w := maintenanceWindow(windows, m.receipt.sourceOID)
	if !w.SourceOid.Valid {
		if absentOK {
			return MaintenanceClosure{}, nil
		}
		return MaintenanceClosure{}, pgerrors.ErrConflict
	}
	if w.OwnerID.Bytes != m.dispatch {
		return MaintenanceClosure{}, pgerrors.ErrConflict
	}
	if w.State != "closed" {
		if err = m.changeCommitted(ctx, "quiesce"); err != nil {
			return MaintenanceClosure{}, err
		}
		rows, windows, err = m.read(ctx)
		if err != nil {
			return MaintenanceClosure{}, err
		}
		if err = m.verify(ctx, rows, windows); err != nil {
			return MaintenanceClosure{}, err
		}
		if err = m.seed.VerifyForWorker(ctx, m.conn); err != nil {
			return MaintenanceClosure{}, err
		}
		if err = m.changeCommitted(ctx, "finish"); err != nil {
			return MaintenanceClosure{}, err
		}
		rows, windows, err = m.read(ctx)
		if err != nil {
			return MaintenanceClosure{}, err
		}
		w = maintenanceWindow(windows, m.receipt.sourceOID)
	}
	if err = m.verify(ctx, rows, windows); err != nil {
		return MaintenanceClosure{}, err
	}
	if err = m.seed.VerifyForWorker(ctx, m.conn); err != nil {
		return MaintenanceClosure{}, err
	}
	return MaintenanceClosure{m.fingerprint, m.receipt.sourceOID, m.database.OID, m.dispatch, m.receipt.createdAt, w.OpenedAt.Time, w.ClosedAt.Time}, nil
}
func maintenanceCallbackError(ctx context.Context, err error) error {
	return authorization(ctx, copyarchive.RestoreTarget{}, func(context.Context, copyarchive.RestoreTarget) error { return err })
}
