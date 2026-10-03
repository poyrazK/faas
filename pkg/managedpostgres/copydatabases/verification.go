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

// VerificationClosure attests only to restoration of original closed admission
// and configuration after a separately owned access window. It is not evidence
// of import execution, data equivalence, or stage readiness.
type VerificationClosure struct {
	planFingerprint                                                          string
	sourceOID, targetOID                                                     uint32
	ownerID, importOwnerID                                                   uuid.UUID
	preparationCreatedAt, importOpenedAt, importClosedAt, openedAt, closedAt time.Time
}

func (VerificationClosure) String() string     { return "private PostgreSQL verification access closure" }
func (c VerificationClosure) GoString() string { return c.String() }
func (c VerificationClosure) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct{ Closed bool }{!c.closedAt.IsZero()})
}
func (c VerificationClosure) ClosedAt() time.Time { return c.closedAt }

// VerificationRun must synchronously borrow the supplied child target, use its
// WithReadOnly helper for inspection, and close every child connection before
// returning. The bootstrap principal remains a trusted worker capability, not a
// customer credential. Independent comparison and durable proof are separate.
type VerificationRun func(context.Context, VerificationTarget) error

// WithVerificationAccess opens at most one window owned separately from the
// original closed import dispatch. Replays recover close-only and return
// conflict; they never execute the read callback again. An uncertain import may
// be inspected after its admission is closed without redispatching any restore.
func (r Receipt) WithVerificationAccess(ctx context.Context, conn *pgx.Conn, source copyinventory.ExportPlan, imported, owner uuid.UUID, authorize copyroles.Authorize, run VerificationRun) (result VerificationClosure, err error) {
	if run == nil {
		return result, pgerrors.ErrInvalid
	}
	v, err := newVerification(r, conn, source, imported, owner, authorize)
	if err != nil {
		return result, err
	}
	if err = v.lock(ctx); err != nil {
		return result, err
	}
	defer releaseLock(ctx, conn, v.q)
	rows, windows, parent, err := v.read(ctx)
	if err != nil {
		return result, err
	}
	if w := verificationWindow(windows, r.sourceOID); w.SourceOid.Valid {
		if w.OwnerID.Bytes != owner {
			return result, pgerrors.ErrConflict
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.PostgresCopyMaintenanceCleanupTimeout)
		defer cancel()
		_, closeErr := v.closeOwned(cleanup, false)
		return result, errors.Join(pgerrors.ErrConflict, closeErr)
	}
	for _, w := range windows {
		if w.State != "closed" {
			return result, pgerrors.ErrConflict
		}
	}
	if err = v.check(ctx); err != nil {
		return result, err
	}
	if err = v.verify(ctx, rows, windows); err != nil {
		return result, err
	}
	absentOK := true
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.PostgresCopyMaintenanceCleanupTimeout)
		defer cancel()
		closed, closeErr := v.closeOwned(cleanup, absentOK)
		if closeErr != nil {
			result = VerificationClosure{}
			err = errors.Join(err, fmt.Errorf("close database verification access: %w", closeErr))
			return
		}
		if err == nil {
			if err = v.check(ctx); err == nil {
				result = closed
			}
		}
	}()
	if err = v.open(ctx, parent); err != nil {
		return result, err
	}
	absentOK = false
	rows, windows, _, err = v.read(ctx)
	if err != nil {
		return result, err
	}
	if err = v.verify(ctx, rows, windows); err != nil {
		return result, err
	}
	if err = v.check(ctx); err != nil {
		return result, err
	}
	child, _ := r.TargetForWorker()
	target := VerificationTarget{v, child, verificationWindow(windows, r.sourceOID).OpenedAt.Time}
	if err = run(ctx, target); err != nil {
		return result, maintenanceCallbackError(ctx, err)
	}
	if err = v.check(ctx); err != nil {
		return result, err
	}
	private, e := v.q.CopyDatabaseMaintenanceSessionsPrivate(ctx, conn, sqlc.CopyDatabaseMaintenanceSessionsPrivateParams{Column1: oid(v.database.OID), Column2: int32(api.PostgresCopyMaintenanceConnections)})
	if e != nil {
		return result, classify(ctx, e)
	}
	if !private.Valid || !private.Bool {
		return result, pgerrors.ErrConflict
	}
	rows, windows, _, err = v.read(ctx)
	if err == nil {
		err = v.verify(ctx, rows, windows)
	}
	return result, err
}

// CloseVerificationAccess never opens admission, invokes a callback, or modifies
// the retained import window. It requires the exact original preparation and
// both owners. A successful replay preserves the first SQL closure timestamp.
func (r Receipt) CloseVerificationAccess(ctx context.Context, conn *pgx.Conn, source copyinventory.ExportPlan, imported, owner uuid.UUID, authorize copyroles.Authorize) (VerificationClosure, error) {
	v, err := newVerification(r, conn, source, imported, owner, authorize)
	if err != nil {
		return VerificationClosure{}, err
	}
	if err = v.lock(ctx); err != nil {
		return VerificationClosure{}, err
	}
	defer releaseLock(ctx, conn, v.q)
	c, err := v.closeOwned(ctx, false)
	if err == nil {
		err = authorization(ctx, v.target, authorize)
	}
	if err != nil {
		return VerificationClosure{}, err
	}
	return c, nil
}

type verification struct {
	*maintenance
	owner uuid.UUID
}

func newVerification(r Receipt, conn *pgx.Conn, source copyinventory.ExportPlan, imported, owner uuid.UUID, authorize copyroles.Authorize) (*verification, error) {
	m, err := newMaintenance(r, conn, source, imported, authorize)
	if err != nil {
		return nil, err
	}
	if owner == uuid.Nil || owner == imported {
		return nil, pgerrors.ErrInvalid
	}
	t := m.target
	for _, id := range []string{t.OwnerID, t.Scope.OperationID, t.Scope.AccountID, t.Scope.ProjectID, t.Scope.SourceDatabaseID, t.Scope.CaptureDatabaseID} {
		if owner.String() == id {
			return nil, pgerrors.ErrInvalid
		}
	}
	return &verification{m, owner}, nil
}

// Every row is bound to the original completed preparation and the exact first
// closed import window. Ordinary import readers call this too, rejecting any
// active verification owner even when its closing catalogue equals the baseline.
func readVerificationJournal(ctx context.Context, m *maintenance, rows []sqlc.GregaleCopyDatabasesDatabase, imported []sqlc.GregaleCopyDatabaseMaintenanceWindow) ([]sqlc.GregaleCopyDatabaseVerificationWindow, error) {
	present, err := m.q.CopyDatabaseVerificationSchemaExists(ctx, m.conn)
	if err != nil {
		return nil, classify(ctx, err)
	}
	if !present {
		return nil, nil
	}
	private, err := m.q.PrivateCopyDatabaseVerificationJournal(ctx, m.conn)
	if err != nil {
		return nil, classify(ctx, err)
	}
	if !private {
		return nil, pgerrors.ErrConflict
	}
	windows, err := m.q.CopyDatabaseVerificationWindows(ctx, m.conn)
	if err != nil {
		return nil, classify(ctx, err)
	}
	seen, owners, targets := map[uint32]bool{}, map[uuid.UUID]bool{}, map[uint32]bool{}
	active := 0
	for _, w := range windows {
		d, _, e := m.receipt.plan.creationDatabase(w.SourceOid.Uint32)
		made := receiptRow(rows, w.SourceOid.Uint32)
		parent := maintenanceWindow(imported, w.SourceOid.Uint32)
		if e != nil || !w.SourceOid.Valid || !w.TargetOid.Valid || !w.OwnerID.Valid || w.OwnerID.Bytes == uuid.Nil || !w.ImportOwnerID.Valid || w.ImportOwnerID.Bytes == uuid.Nil || w.ImportOwnerID.Bytes == w.OwnerID.Bytes || seen[w.SourceOid.Uint32] || owners[w.OwnerID.Bytes] || targets[w.TargetOid.Uint32] || w.TargetOid.Uint32 != d.OID || d.AllowConnections || d.OID == m.target.DatabaseOID || w.PlanFingerprint != m.fingerprint || !made.CreatedAt.Valid || (made.State != "created" && made.State != "existing") || !finiteSQLTime(w.PreparationCreatedAt) || !w.PreparationCreatedAt.Time.Equal(made.CreatedAt.Time) || !parent.SourceOid.Valid || parent.State != "closed" || parent.OwnerID.Bytes != w.ImportOwnerID.Bytes || !finiteSQLTime(w.ImportOpenedAt) || !w.ImportOpenedAt.Time.Equal(parent.OpenedAt.Time) || !finiteSQLTime(w.ImportClosedAt) || !w.ImportClosedAt.Time.Equal(parent.ClosedAt.Time) || !finiteSQLTime(w.OpenedAt) || w.OpenedAt.Time.Before(w.ImportClosedAt.Time) {
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
func verificationWindow(rows []sqlc.GregaleCopyDatabaseVerificationWindow, id uint32) sqlc.GregaleCopyDatabaseVerificationWindow {
	for _, r := range rows {
		if r.SourceOid.Uint32 == id {
			return r
		}
	}
	return sqlc.GregaleCopyDatabaseVerificationWindow{}
}
func (v *verification) read(ctx context.Context) ([]sqlc.GregaleCopyDatabasesDatabase, []sqlc.GregaleCopyDatabaseVerificationWindow, sqlc.GregaleCopyDatabaseMaintenanceWindow, error) {
	rows, imported, err := v.readBase(ctx)
	if err != nil {
		return nil, nil, sqlc.GregaleCopyDatabaseMaintenanceWindow{}, err
	}
	parent := maintenanceWindow(imported, v.receipt.sourceOID)
	if !parent.SourceOid.Valid || parent.State != "closed" || parent.OwnerID.Bytes != v.dispatch {
		return nil, nil, parent, pgerrors.ErrConflict
	}
	for _, w := range imported {
		if w.State != "closed" {
			return nil, nil, parent, pgerrors.ErrConflict
		}
	}
	windows, err := readVerificationJournal(ctx, v.maintenance, rows, imported)
	return rows, windows, parent, err
}
func (v *verification) verify(ctx context.Context, rows []sqlc.GregaleCopyDatabasesDatabase, windows []sqlc.GregaleCopyDatabaseVerificationWindow) error {
	overrides := map[uint32]copyinventory.Database{}
	for _, w := range windows {
		if w.State == "closed" {
			continue
		}
		d, _, _ := v.receipt.plan.creationDatabase(w.SourceOid.Uint32)
		d.AllowConnections, d.Template, d.ConnectionLimit = w.State == "open", false, int32(api.PostgresCopyMaintenanceConnections)
		overrides[d.OID] = d
	}
	present, err := verifyCatalogueExpected(ctx, v.conn, v.receipt.plan, rows, v.receipt.sourceOID, overrides)
	if err != nil {
		return err
	}
	if !present {
		return pgerrors.ErrConflict
	}
	return nil
}
func (v *verification) params(action string, parent sqlc.GregaleCopyDatabaseMaintenanceWindow) sqlc.ChangeCopyDatabaseVerificationAdmissionParams {
	a := v.maintenance.params(action)
	return sqlc.ChangeCopyDatabaseVerificationAdmissionParams{SourceOid: a.SourceOid, TargetOid: a.TargetOid, DatabaseName: a.DatabaseName, OwnerOid: a.OwnerOid, VerificationOwner: pgtype.UUID{Bytes: v.owner, Valid: true}, PlanFingerprint: a.PlanFingerprint, PreparedAt: a.PreparedAt, Action: action, OriginalTemplate: a.OriginalTemplate, OriginalLimit: a.OriginalLimit, VerificationLimit: a.MaintenanceLimit, ImportOwner: parent.OwnerID, ImportOpened: parent.OpenedAt, ImportClosed: parent.ClosedAt}
}
func (v *verification) open(ctx context.Context, parent sqlc.GregaleCopyDatabaseMaintenanceWindow) error {
	if err := v.check(ctx); err != nil {
		return err
	}
	tx, err := v.conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadWrite})
	if err != nil {
		return classify(ctx, err)
	}
	defer rollback(ctx, tx)
	present, err := v.q.CopyDatabaseVerificationSchemaExists(ctx, tx)
	if err != nil {
		return classify(ctx, err)
	}
	if !present {
		for _, install := range []func(context.Context, sqlc.DBTX) error{v.q.InstallCopyDatabaseVerificationSchema, v.q.InstallCopyDatabaseVerificationWindows, v.q.InstallCopyDatabaseVerificationActiveIndex} {
			if err = install(ctx, tx); err != nil {
				return classify(ctx, err)
			}
		}
	}
	private, err := v.q.PrivateCopyDatabaseVerificationJournal(ctx, tx)
	if err != nil {
		return classify(ctx, err)
	}
	if !private {
		return pgerrors.ErrConflict
	}
	a := v.params("open", parent)
	if err = v.q.InsertCopyDatabaseVerificationWindow(ctx, tx, sqlc.InsertCopyDatabaseVerificationWindowParams{Column1: a.SourceOid, Column2: a.TargetOid, Column3: a.VerificationOwner, Column4: a.PlanFingerprint, Column5: a.PreparedAt, Column6: a.ImportOwner, Column7: a.ImportOpened, Column8: a.ImportClosed}); err != nil {
		return classify(ctx, err)
	}
	if err = v.change(ctx, tx, "open", parent); err != nil {
		return err
	}
	if err = authorization(ctx, v.target, v.authorize); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return classify(ctx, err)
	}
	return nil
}
func (v *verification) change(ctx context.Context, tx pgx.Tx, action string, parent sqlc.GregaleCopyDatabaseMaintenanceWindow) error {
	if err := v.q.InstallCopyDatabaseVerificationMutation(ctx, tx); err != nil {
		return classify(ctx, err)
	}
	ok, err := v.q.ChangeCopyDatabaseVerificationAdmission(ctx, tx, v.params(action, parent))
	if err != nil {
		return classify(ctx, err)
	}
	if !ok {
		return pgerrors.ErrConflict
	}
	return nil
}
func (v *verification) changeCommitted(ctx context.Context, action string, parent sqlc.GregaleCopyDatabaseMaintenanceWindow) error {
	tx, err := v.conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadWrite})
	if err != nil {
		return classify(ctx, err)
	}
	defer rollback(ctx, tx)
	if err = v.change(ctx, tx, action, parent); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return classify(ctx, err)
	}
	return nil
}
func (v *verification) closeOwned(ctx context.Context, absentOK bool) (VerificationClosure, error) {
	rows, windows, parent, err := v.read(ctx)
	if err != nil {
		return VerificationClosure{}, err
	}
	w := verificationWindow(windows, v.receipt.sourceOID)
	if !w.SourceOid.Valid {
		if absentOK {
			return VerificationClosure{}, nil
		}
		return VerificationClosure{}, pgerrors.ErrConflict
	}
	if w.OwnerID.Bytes != v.owner {
		return VerificationClosure{}, pgerrors.ErrConflict
	}
	if w.State != "closed" {
		// Quiesce before full catalogue/role checks; a leaked child is never killed.
		if err = v.changeCommitted(ctx, "quiesce", parent); err != nil {
			return VerificationClosure{}, err
		}
		rows, windows, parent, err = v.read(ctx)
		if err != nil {
			return VerificationClosure{}, err
		}
		if err = v.verify(ctx, rows, windows); err != nil {
			return VerificationClosure{}, err
		}
		if err = v.seed.VerifyForWorker(ctx, v.conn); err != nil {
			return VerificationClosure{}, err
		}
		if err = v.changeCommitted(ctx, "finish", parent); err != nil {
			return VerificationClosure{}, err
		}
		rows, windows, _, err = v.read(ctx)
		if err != nil {
			return VerificationClosure{}, err
		}
		w = verificationWindow(windows, v.receipt.sourceOID)
	}
	if err = v.verify(ctx, rows, windows); err != nil {
		return VerificationClosure{}, err
	}
	if err = v.seed.VerifyForWorker(ctx, v.conn); err != nil {
		return VerificationClosure{}, err
	}
	return VerificationClosure{v.fingerprint, v.receipt.sourceOID, v.database.OID, v.owner, v.dispatch, v.receipt.createdAt, w.ImportOpenedAt.Time, w.ImportClosedAt.Time, w.OpenedAt.Time, w.ClosedAt.Time}, nil
}

// Strict preparation/create readers may never accept active import or verification
// ownership merely because quiescing happened to restore the same SQL settings.
// Caller holds the shared bootstrap lock and has authenticated the role seed.
func verifyClosedAccessJournals(ctx context.Context, conn *pgx.Conn, p Plan, rows []sqlc.GregaleCopyDatabasesDatabase) error {
	fp, err := preparationPlanFingerprint(p)
	if err != nil {
		return err
	}
	m := &maintenance{receipt: Receipt{plan: p}, conn: conn, q: sqlc.New(), target: copyarchive.RestoreTarget(p.body.Target), fingerprint: fp}
	imported, err := m.readMaintenanceJournal(ctx, rows)
	if err != nil {
		return err
	}
	for _, w := range imported {
		if w.State != "closed" {
			return pgerrors.ErrConflict
		}
	}
	windows, err := readVerificationJournal(ctx, m, rows, imported)
	if err != nil {
		return err
	}
	for _, w := range windows {
		if w.State != "closed" {
			return pgerrors.ErrConflict
		}
	}
	return nil
}
