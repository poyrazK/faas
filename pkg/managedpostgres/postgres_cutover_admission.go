package managedpostgres

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// CutoverAdmissionFence stops new VM admissions across every scope of an app.
// It is not evidence that existing VMs, requests, or database sessions stopped.
type CutoverAdmissionFence struct {
	CutoverID, AppID string
	FencedAt         time.Time
}

// FenceCutoverAdmission is internal controller plumbing. Prepare and Verify
// never call it; no customer activation endpoint is enabled by this primitive.
// The barrier has no lease expiry. Only completed cancellation can lift it in
// this slice, after every staged provider credential has been revoked.
func (s *PostgresStore) FenceCutoverAdmission(ctx context.Context, account, id string) (CutoverAdmissionFence, error) {
	q := sqlc.New()
	initial, err := q.GetManagedPostgresCutover(ctx, s.pool, sqlc.GetManagedPostgresCutoverParams{AccountID: account, ID: id})
	if err != nil {
		return CutoverAdmissionFence{}, mapPostgresError(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CutoverAdmissionFence{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	owner, err := q.LockManagedPostgresCutoverAccount(ctx, tx, account)
	if err != nil {
		return CutoverAdmissionFence{}, mapPostgresError(err)
	}
	appID := cutoverUUID(initial.AppID)
	app, err := q.LockManagedPostgresCutoverAdmissionApp(ctx, tx, appID)
	if err != nil {
		return CutoverAdmissionFence{}, mapPostgresError(err)
	}
	if app.AccountID != account || owner == "deleted_pending" || (app.Status != "active" && app.Status != "evicted_cold") {
		return CutoverAdmissionFence{}, ErrConflict
	}
	c, err := q.LockManagedPostgresCutoverForVerification(ctx, tx, sqlc.LockManagedPostgresCutoverForVerificationParams{AccountID: account, ID: id})
	if err != nil {
		return CutoverAdmissionFence{}, mapPostgresError(err)
	}
	if c.State != string(CutoverVerified) && c.State != string(CutoverVerifying) {
		return CutoverAdmissionFence{}, ErrConflict
	}
	fence := CutoverAdmissionFence{CutoverID: id, AppID: appID, FencedAt: bindingDeliveryTime(app.ManagedPostgresAdmissionFencedAt)}
	if app.ManagedPostgresAdmissionCutoverID.Valid {
		if cutoverUUID(app.ManagedPostgresAdmissionCutoverID) != id {
			return CutoverAdmissionFence{}, ErrConflict
		}
	} else {
		// The database trigger checks every member's verification against its
		// own clock after the locks, including time spent waiting for them.
		stamp, err := q.FenceManagedPostgresCutoverAdmission(ctx, tx, sqlc.FenceManagedPostgresCutoverAdmissionParams{AppID: appID, CutoverID: id})
		if err != nil {
			return CutoverAdmissionFence{}, mapPostgresError(err)
		}
		fence.FencedAt = bindingDeliveryTime(stamp)
	}
	return fence, mapPostgresError(tx.Commit(ctx))
}
