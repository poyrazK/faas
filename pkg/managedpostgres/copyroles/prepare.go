package copyroles

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	inventorysql "github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory/sqlc"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyroles/sqlc"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

// Authorize synchronously checks the live operation lease and durable dispatch
// owner for the exact independently authenticated target. The enclosing provider
// borrower must also check physical placement before and after this operation.
type Authorize func(context.Context, copyarchive.RestoreTarget) error

type roleIdentity struct {
	SourceOID uint32 `json:"source_oid"`
	TargetOID uint32 `json:"target_oid"`
}
type privateReceipt struct {
	Created  []roleIdentity `json:"created_roles"`
	SeededAt time.Time      `json:"seeded_at"`
}
type Receipt struct {
	plan Plan
	body privateReceipt
}

func (Receipt) String() string                 { return "private PostgreSQL role seed receipt" }
func (r Receipt) GoString() string             { return r.String() }
func (r Receipt) MarshalJSON() ([]byte, error) { return json.Marshal(r.plan.Summary()) }
func (r Receipt) SeededAt() time.Time          { return r.body.SeededAt }

// Identity is private input for later database/ownership materialization. Original
// login intent is not an activation or credential receipt.
type Identity struct {
	SourceOID, TargetOID uint32
	DesiredLogin         bool
}

func (Identity) String() string     { return "private PostgreSQL seeded role identity" }
func (i Identity) GoString() string { return i.String() }
func (i Identity) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct{ OriginalLogin bool }{i.DesiredLogin})
}
func (r Receipt) IdentitiesForWorker() []Identity {
	if r.validate() != nil {
		return nil
	}
	created := map[uint32]uint32{}
	for _, x := range r.body.Created {
		created[x.SourceOID] = x.TargetOID
	}
	result := make([]Identity, 0, len(r.plan.body.Roles))
	for _, x := range r.plan.body.Roles {
		id := x.ExistingTargetOID
		if id == 0 {
			id = created[x.Source.OID]
		}
		result = append(result, Identity{SourceOID: x.Source.OID, TargetOID: id, DesiredLogin: x.Source.Login})
	}
	return result
}
func (r Receipt) validate() error {
	if r.plan.validate() != nil || r.body.SeededAt.IsZero() || len(r.body.Created) != r.plan.Summary().Created {
		return pgerrors.ErrConflict
	}
	bySource, used := map[uint32]uint32{}, map[uint32]bool{}
	for _, x := range r.plan.body.Baseline {
		used[x.OID] = true
	}
	for _, x := range r.body.Created {
		if x.SourceOID == 0 || x.TargetOID == 0 || bySource[x.SourceOID] != 0 || used[x.TargetOID] {
			return pgerrors.ErrConflict
		}
		bySource[x.SourceOID] = x.TargetOID
		used[x.TargetOID] = true
	}
	for _, x := range r.plan.body.Roles {
		if (x.ExistingTargetOID == 0) != (bySource[x.Source.OID] != 0) {
			return pgerrors.ErrConflict
		}
	}
	return nil
}

// Prepare seeds roles and records their SQL OIDs in the same target transaction.
// Exact retries verify the immutable receipt/catalogue and perform no role DDL.
// Cancellation or a lost commit reply returns no successful receipt. The caller
// retains ownership and may recover using the original encrypted plan and pins.
// No role password, source connection, membership grant, dataset publication or
// stage readiness is produced here. The caller owns this dedicated connection.
func Prepare(ctx context.Context, conn *pgx.Conn, plan Plan, authorize Authorize) (Receipt, error) {
	var zero Receipt
	if authorize == nil {
		return zero, pgerrors.ErrInvalid
	}
	raw, err := plan.PrivatePayloadForSealing()
	if err != nil {
		return zero, err
	}
	target := copyarchive.RestoreTarget(plan.body.Target)
	if err := authenticate(ctx, conn, target); err != nil {
		return zero, err
	}
	if err := authorization(ctx, target, authorize); err != nil {
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
	if err := q.LockRoleSeed(ctx, tx); err != nil {
		return zero, classify(ctx, err)
	}
	if err := authorization(ctx, target, authorize); err != nil {
		return zero, err
	}
	present, err := q.RoleSeedSchemaExists(ctx, tx)
	if err != nil {
		return zero, classify(ctx, err)
	}
	if !present {
		if err := q.InstallRoleSeedSchema(ctx, tx); err != nil {
			return zero, classify(ctx, err)
		}
		if err := q.InstallRoleSeedReceipt(ctx, tx); err != nil {
			return zero, classify(ctx, err)
		}
	}
	private, err := q.PrivateRoleSeedReceipt(ctx, tx)
	if err != nil {
		return zero, classify(ctx, err)
	}
	if !private {
		return zero, pgerrors.ErrConflict
	}
	if err := q.InstallRoleSeedAssertion(ctx, tx); err != nil {
		return zero, classify(ctx, err)
	}
	if err := q.InstallRoleSeedConfigFunction(ctx, tx); err != nil {
		return zero, classify(ctx, err)
	}
	if err := q.InstallRoleSeedFunction(ctx, tx); err != nil {
		return zero, classify(ctx, err)
	}
	result, err := q.SeedTargetRoles(ctx, tx, raw)
	if err != nil {
		return zero, classify(ctx, err)
	}
	receipt := Receipt{plan: plan}
	if json.Unmarshal(result, &receipt.body) != nil || receipt.validate() != nil {
		return zero, pgerrors.ErrConflict
	}
	if err := authorization(ctx, target, authorize); err != nil {
		return zero, err
	}
	if err := tx.Commit(ctx); err != nil {
		return zero, classify(ctx, err)
	}
	if err := authenticate(ctx, conn, target); err != nil {
		return zero, err
	}
	if err := authorization(ctx, target, authorize); err != nil {
		return zero, err
	}
	return receipt, nil
}

func authenticate(ctx context.Context, conn *pgx.Conn, t copyarchive.RestoreTarget) error {
	if conn == nil || conn.IsClosed() || conn.PgConn().IsBusy() || conn.PgConn().TxStatus() != 'I' || conn.Config().User != t.RoleName || conn.Config().Database != t.DatabaseName {
		return pgerrors.ErrConflict
	}
	a, err := inventorysql.New().CopyClusterIdentity(ctx, conn)
	if err != nil {
		return classify(ctx, err)
	}
	if int(a.ServerVersion/10000) != t.Scope.PostgresMajor || a.DatabaseName != t.DatabaseName || !a.DatabaseOid.Valid || a.DatabaseOid.Uint32 != t.DatabaseOID ||
		a.RoleName != t.RoleName || a.SessionRole != t.RoleName || !a.RoleOid.Valid || a.RoleOid.Uint32 != t.RoleOID || a.ReadOnly {
		return pgerrors.ErrConflict
	}
	return ctx.Err()
}
func authorization(ctx context.Context, t copyarchive.RestoreTarget, authorize Authorize) error {
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
	var e *pgconn.PgError
	if errors.As(err, &e) {
		switch e.Code {
		case "55000", "42710":
			return pgerrors.ErrConflict
		case "42501", "0A000":
			return pgerrors.ErrUnsupported
		case "22023":
			return pgerrors.ErrInvalid
		}
	}
	return pgerrors.ErrUnavailable
}
