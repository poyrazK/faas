package copydatabases

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copydatabases/sqlc"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyroles"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

type verificationWindowRow struct {
	sqlc.GregaleCopyDatabaseVerificationWindow
	Attempt                            int32
	PreviousOwnerID                    pgtype.UUID
	PreviousOpenedAt, PreviousClosedAt pgtype.Timestamptz
}
type verificationRetry struct {
	previous VerificationClosure
	number   int32
}

// The coordinator must durably admit a new owner before calling this and must
// never retry a compared/verified owner. Native closure alone does not prove a
// failed comparison or grant CPU/spool/billing admission. Every attempt is new;
// no old window is reopened, and replay remains close-only.
func (r Receipt) WithVerificationRetryAccess(ctx context.Context, conn *pgx.Conn, source copyinventory.ExportPlan,
	imported, owner uuid.UUID, previous VerificationClosure, authorize copyroles.Authorize, run VerificationRun) (VerificationClosure, error) {
	if run == nil {
		return VerificationClosure{}, pgerrors.ErrInvalid
	}
	v, err := newVerificationRetry(r, conn, source, imported, owner, previous, authorize)
	if err != nil {
		return VerificationClosure{}, err
	}
	return v.withAccess(ctx, run, nil)
}

// WithVerificationRetryAccessAdmitted adds durable work admission after the
// exact closed predecessor and never-opened successor are authenticated under
// the shared lock. An existing successor never consumes a new admission.
func (r Receipt) WithVerificationRetryAccessAdmitted(ctx context.Context, conn *pgx.Conn, source copyinventory.ExportPlan,
	imported, owner uuid.UUID, previous VerificationClosure, authorize, admit copyroles.Authorize, run VerificationRun) (VerificationClosure, error) {
	if run == nil || admit == nil {
		return VerificationClosure{}, pgerrors.ErrInvalid
	}
	v, err := newVerificationRetry(r, conn, source, imported, owner, previous, authorize)
	if err != nil {
		return VerificationClosure{}, err
	}
	return v.withAccess(ctx, run, admit)
}

func newVerificationRetry(r Receipt, conn *pgx.Conn, source copyinventory.ExportPlan, imported, owner uuid.UUID,
	previous VerificationClosure, authorize copyroles.Authorize) (*verification, error) {
	if !previous.MatchesForWorker(r, imported, previous.ownerID, previous.openedAt) || previous.ownerID == owner {
		return nil, pgerrors.ErrInvalid
	}
	if previous.attempt >= api.PostgresCopyVerificationAttemptsMax {
		return nil, pgerrors.ErrQuotaExceeded
	}
	v, err := newVerification(r, conn, source, imported, owner, authorize)
	if err != nil {
		return nil, err
	}
	v.retry = &verificationRetry{previous, previous.attempt + 1}
	return v, nil
}

// Recover the exact held retry close-only without recovering its predecessor as
// a publishable closure while child access is open. Owner references are only
// assertions: authenticated native history supplies all attempt/time bindings.
// NotFound means the selected owner has no native row, never permission to open.
func (r Receipt) CloseVerificationRetryAccess(ctx context.Context, conn *pgx.Conn, source copyinventory.ExportPlan,
	imported, owner, previousOwner uuid.UUID, authorize copyroles.Authorize) (VerificationClosure, error) {
	v, err := newVerification(r, conn, source, imported, owner, authorize)
	if err != nil {
		return VerificationClosure{}, err
	}
	if previousOwner == uuid.Nil || previousOwner == owner || previousOwner == imported {
		return VerificationClosure{}, pgerrors.ErrInvalid
	}
	if err = v.lock(ctx); err != nil {
		return VerificationClosure{}, err
	}
	defer releaseLock(ctx, conn, v.q)
	_, windows, _, err := v.read(ctx)
	if err != nil {
		return VerificationClosure{}, err
	}
	w := verificationOwnedWindow(windows, r.sourceOID, owner)
	if !w.SourceOid.Valid {
		return VerificationClosure{}, pgerrors.ErrNotFound
	}
	prior := verificationOwnedWindow(windows, r.sourceOID, previousOwner)
	if w.Attempt < 2 || w.PreviousOwnerID.Bytes != previousOwner || !prior.SourceOid.Valid || prior.State != "closed" || prior.Attempt+1 != w.Attempt {
		return VerificationClosure{}, pgerrors.ErrConflict
	}
	v.retry = &verificationRetry{v.closure(prior), w.Attempt}
	c, err := v.closeOwned(ctx, false)
	if err == nil {
		err = authorization(ctx, v.target, v.authorize)
	}
	if err != nil {
		return VerificationClosure{}, err
	}
	return c, nil
}

func (v *verification) attempt() int32 {
	if v.retry == nil {
		return 1
	}
	return v.retry.number
}
func verificationOwnedWindow(rows []verificationWindowRow, source uint32, owner uuid.UUID) verificationWindowRow {
	for _, w := range rows {
		if w.SourceOid.Uint32 == source && w.OwnerID.Bytes == owner {
			return w
		}
	}
	return verificationWindowRow{}
}
func (v *verification) ownedWindow(rows []verificationWindowRow) verificationWindowRow {
	if v.retry == nil {
		return verificationWindow(rows, v.receipt.sourceOID)
	}
	return verificationOwnedWindow(rows, v.receipt.sourceOID, v.owner)
}
func (v *verification) closure(w verificationWindowRow) VerificationClosure {
	return VerificationClosure{v.fingerprint, w.SourceOid.Uint32, w.TargetOid.Uint32, w.OwnerID.Bytes, w.ImportOwnerID.Bytes,
		w.PreparationCreatedAt.Time, w.ImportOpenedAt.Time, w.ImportClosedAt.Time, w.OpenedAt.Time, w.ClosedAt.Time, w.Attempt}
}
func (v *verification) checkRetryPosition(rows []verificationWindowRow) error {
	for _, w := range rows {
		if w.OwnerID.Bytes == v.owner || w.ImportOwnerID.Bytes == v.owner {
			return pgerrors.ErrConflict
		}
	}
	if v.retry == nil {
		return nil
	}
	var last verificationWindowRow
	for _, w := range rows {
		if w.SourceOid.Uint32 == v.receipt.sourceOID && w.Attempt > last.Attempt {
			last = w
		}
	}
	p := v.retry.previous
	if !last.SourceOid.Valid || last.State != "closed" || last.Attempt+1 != v.retry.number || last.OwnerID.Bytes != p.ownerID ||
		!last.OpenedAt.Time.Equal(p.openedAt) || !last.ClosedAt.Time.Equal(p.closedAt) {
		return pgerrors.ErrConflict
	}
	return nil
}

// Original first windows are validated before this function. Each retry extends
// that exact chain by one; gaps, forks, reused owners and changed native parents
// fail every protocol reader, including ordinary import/preparation readers.
func readVerificationRetries(ctx context.Context, m *maintenance, original []verificationWindowRow) ([]verificationWindowRow, error) {
	present, err := m.q.CopyDatabaseVerificationRetrySchemaExists(ctx, m.conn)
	if err != nil {
		return nil, classify(ctx, err)
	}
	if !present {
		return original, nil
	}
	private, err := m.q.PrivateCopyDatabaseVerificationRetryJournal(ctx, m.conn)
	if err != nil {
		return nil, classify(ctx, err)
	}
	if !private {
		return nil, pgerrors.ErrConflict
	}
	retries, err := m.q.CopyDatabaseVerificationRetryWindows(ctx, m.conn)
	if err != nil {
		return nil, classify(ctx, err)
	}
	last := map[uint32]verificationWindowRow{}
	owners := map[uuid.UUID]bool{}
	active := 0
	for _, w := range original {
		last[w.SourceOid.Uint32] = w
		owners[w.OwnerID.Bytes] = true
		owners[w.ImportOwnerID.Bytes] = true
		if w.State != "closed" {
			active++
		}
	}
	windows := append([]verificationWindowRow(nil), original...)
	for _, r := range retries {
		p := last[r.SourceOid.Uint32]
		if !r.SourceOid.Valid || !r.TargetOid.Valid || !r.OwnerID.Valid || r.OwnerID.Bytes == uuid.Nil || owners[r.OwnerID.Bytes] ||
			!p.SourceOid.Valid || p.State != "closed" || r.Attempt != p.Attempt+1 || r.Attempt > api.PostgresCopyVerificationAttemptsMax ||
			r.TargetOid.Uint32 != p.TargetOid.Uint32 || r.PlanFingerprint != p.PlanFingerprint || !finiteSQLTime(r.PreparationCreatedAt) || !r.PreparationCreatedAt.Time.Equal(p.PreparationCreatedAt.Time) ||
			!r.ImportOwnerID.Valid || r.ImportOwnerID.Bytes != p.ImportOwnerID.Bytes || !finiteSQLTime(r.ImportOpenedAt) || !r.ImportOpenedAt.Time.Equal(p.ImportOpenedAt.Time) ||
			!finiteSQLTime(r.ImportClosedAt) || !r.ImportClosedAt.Time.Equal(p.ImportClosedAt.Time) || !r.PreviousOwnerID.Valid || r.PreviousOwnerID.Bytes != p.OwnerID.Bytes ||
			!finiteSQLTime(r.PreviousOpenedAt) || !r.PreviousOpenedAt.Time.Equal(p.OpenedAt.Time) || !finiteSQLTime(r.PreviousClosedAt) || !r.PreviousClosedAt.Time.Equal(p.ClosedAt.Time) ||
			!finiteSQLTime(r.OpenedAt) || r.OpenedAt.Time.Before(p.ClosedAt.Time) {
			return nil, pgerrors.ErrConflict
		}
		switch r.State {
		case "open", "closing":
			active++
			if r.ClosedAt.Valid {
				return nil, pgerrors.ErrConflict
			}
		case "closed":
			if !finiteSQLTime(r.ClosedAt) || r.ClosedAt.Time.Before(r.OpenedAt.Time) {
				return nil, pgerrors.ErrConflict
			}
		default:
			return nil, pgerrors.ErrConflict
		}
		w := verificationWindowRow{sqlc.GregaleCopyDatabaseVerificationWindow{SourceOid: r.SourceOid, TargetOid: r.TargetOid, OwnerID: r.OwnerID, PlanFingerprint: r.PlanFingerprint,
			PreparationCreatedAt: r.PreparationCreatedAt, State: r.State, OpenedAt: r.OpenedAt, ClosedAt: r.ClosedAt, ImportOwnerID: r.ImportOwnerID, ImportOpenedAt: r.ImportOpenedAt, ImportClosedAt: r.ImportClosedAt},
			r.Attempt, r.PreviousOwnerID, r.PreviousOpenedAt, r.PreviousClosedAt}
		last[r.SourceOid.Uint32] = w
		owners[r.OwnerID.Bytes] = true
		windows = append(windows, w)
	}
	if active > 1 {
		return nil, pgerrors.ErrConflict
	}
	return windows, nil
}

func (v *verification) retryParams(action string, parent sqlc.GregaleCopyDatabaseMaintenanceWindow) sqlc.ChangeCopyDatabaseVerificationRetryAdmissionParams {
	a := v.params(action, parent)
	p := v.retry.previous
	return sqlc.ChangeCopyDatabaseVerificationRetryAdmissionParams{SourceOid: a.SourceOid, TargetOid: a.TargetOid, DatabaseName: a.DatabaseName, OwnerOid: a.OwnerOid,
		VerificationOwner: a.VerificationOwner, PlanFingerprint: a.PlanFingerprint, PreparedAt: a.PreparedAt, Action: a.Action, OriginalTemplate: a.OriginalTemplate,
		OriginalLimit: a.OriginalLimit, VerificationLimit: a.VerificationLimit, ImportOwner: a.ImportOwner, ImportOpened: a.ImportOpened, ImportClosed: a.ImportClosed,
		Attempt: v.retry.number, PreviousOwner: pgtype.UUID{Bytes: p.ownerID, Valid: true}, PreviousOpened: pgtype.Timestamptz{Time: p.openedAt, Valid: true}, PreviousClosed: pgtype.Timestamptz{Time: p.closedAt, Valid: true}}
}
func (v *verification) openRetry(ctx context.Context, parent sqlc.GregaleCopyDatabaseMaintenanceWindow) error {
	if err := v.check(ctx); err != nil {
		return err
	}
	tx, err := v.conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadWrite})
	if err != nil {
		return classify(ctx, err)
	}
	defer rollback(ctx, tx)
	present, err := v.q.CopyDatabaseVerificationRetrySchemaExists(ctx, tx)
	if err != nil {
		return classify(ctx, err)
	}
	if !present {
		for _, install := range []func(context.Context, sqlc.DBTX) error{v.q.InstallCopyDatabaseVerificationRetrySchema, v.q.InstallCopyDatabaseVerificationRetryWindows, v.q.InstallCopyDatabaseVerificationRetryActiveIndex} {
			if err = install(ctx, tx); err != nil {
				return classify(ctx, err)
			}
		}
	}
	private, err := v.q.PrivateCopyDatabaseVerificationRetryJournal(ctx, tx)
	if err != nil {
		return classify(ctx, err)
	}
	if !private {
		return pgerrors.ErrConflict
	}
	a := v.retryParams("open", parent)
	err = v.q.InsertCopyDatabaseVerificationRetryWindow(ctx, tx, sqlc.InsertCopyDatabaseVerificationRetryWindowParams{
		Column1: a.SourceOid, Column2: a.TargetOid, Column3: a.VerificationOwner, Column4: a.PlanFingerprint, Column5: a.PreparedAt, Column6: a.ImportOwner,
		Column7: a.ImportOpened, Column8: a.ImportClosed, Column9: a.Attempt, Column10: a.PreviousOwner, Column11: a.PreviousOpened, Column12: a.PreviousClosed})
	if err != nil {
		return classify(ctx, err)
	}
	if err = v.changeRetry(ctx, tx, "open", parent); err != nil {
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
func (v *verification) changeRetry(ctx context.Context, tx pgx.Tx, action string, parent sqlc.GregaleCopyDatabaseMaintenanceWindow) error {
	if err := v.q.InstallCopyDatabaseVerificationRetryMutation(ctx, tx); err != nil {
		return classify(ctx, err)
	}
	ok, err := v.q.ChangeCopyDatabaseVerificationRetryAdmission(ctx, tx, v.retryParams(action, parent))
	if err != nil {
		return classify(ctx, err)
	}
	if !ok {
		return pgerrors.ErrConflict
	}
	return nil
}
