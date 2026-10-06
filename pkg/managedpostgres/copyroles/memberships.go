package copyroles

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyroles/sqlc"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

type MembershipReceipt struct {
	plan MembershipPlan
	at   time.Time
}

func (MembershipReceipt) String() string                 { return "private PostgreSQL membership receipt" }
func (r MembershipReceipt) GoString() string             { return r.String() }
func (r MembershipReceipt) MarshalJSON() ([]byte, error) { return json.Marshal(r.plan.Summary()) }
func (r MembershipReceipt) AppliedAt() time.Time         { return r.at }

// ApplyMemberships reproduces the entire frozen grant graph in one transaction,
// including removals, grantors and all options. It requires dispatch ownership
// of the exact sealed plan and independent provider placement around this call.
// Run after work requiring temporary creator grants and before login activation.
// Exact retries verify the original journal and current graph without GRANT or
// REVOKE. No credential or database dataset readiness is implied by this receipt.
func ApplyMemberships(ctx context.Context, conn *pgx.Conn, plan MembershipPlan, authorize Authorize) (MembershipReceipt, error) {
	var zero MembershipReceipt
	if authorize == nil {
		return zero, pgerrors.ErrInvalid
	}
	raw, err := plan.PrivatePayloadForSealing()
	if err != nil {
		return zero, err
	}
	target := copyarchive.RestoreTarget(plan.body.SeedPlan.Target)
	if err = authenticate(ctx, conn, target); err != nil {
		return zero, err
	}
	if err = authorization(ctx, target, authorize); err != nil {
		return zero, err
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadWrite})
	if err != nil {
		return zero, classify(ctx, err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	q := sqlc.New()
	if err = q.LockRoleSeed(ctx, tx); err != nil {
		return zero, classify(ctx, err)
	}
	if err = authorization(ctx, target, authorize); err != nil {
		return zero, err
	}
	private, err := q.PrivateRoleSeedReceipt(ctx, tx)
	if err != nil || !private {
		return zero, pgerrors.ErrConflict
	}
	present, err := q.MembershipSchemaExists(ctx, tx)
	if err != nil {
		return zero, classify(ctx, err)
	}
	if !present {
		if err = q.InstallMembershipSchema(ctx, tx); err != nil {
			return zero, classify(ctx, err)
		}
		if err = q.InstallMembershipReceipt(ctx, tx); err != nil {
			return zero, classify(ctx, err)
		}
	}
	private, err = q.PrivateMembershipReceipt(ctx, tx)
	if err != nil || !private {
		return zero, pgerrors.ErrConflict
	}
	if err = q.InstallRoleSeedAssertion(ctx, tx); err != nil {
		return zero, classify(ctx, err)
	}
	if err = q.InstallMembershipReadFunction(ctx, tx); err != nil {
		return zero, classify(ctx, err)
	}
	if err = q.InstallMembershipApplyFunction(ctx, tx); err != nil {
		return zero, classify(ctx, err)
	}
	at, err := q.ApplyTargetMemberships(ctx, tx, raw)
	if err != nil {
		return zero, classify(ctx, err)
	}
	if !at.Valid || at.Time.IsZero() {
		return zero, pgerrors.ErrConflict
	}
	if err = authorization(ctx, target, authorize); err != nil {
		return zero, err
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, classify(ctx, err)
	}
	if err = authenticate(ctx, conn, target); err != nil {
		return zero, err
	}
	if err = authorization(ctx, target, authorize); err != nil {
		return zero, err
	}
	return MembershipReceipt{plan: plan, at: at.Time}, nil
}
