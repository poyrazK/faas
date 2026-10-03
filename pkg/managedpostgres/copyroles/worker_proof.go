package copyroles

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyroles/sqlc"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

type workerProof struct {
	Plan    payload        `json:"plan"`
	Receipt privateReceipt `json:"receipt"`
}

func (r Receipt) MatchesSourceForWorker(source copyinventory.ExportPlan) bool {
	if r.validate() != nil {
		return false
	}
	requirements, err := source.RequirementsForWorker()
	if err != nil || len(requirements) == 0 || !requirements[0].Scope.Equal(r.plan.body.Target.Scope) {
		return false
	}
	c, err := source.MembershipCatalogueForWorker()
	return err == nil && membershipSourceMatchesSeed(c, r)
}

func (r Receipt) TargetForWorker() (copyarchive.RestoreTarget, error) {
	if r.validate() != nil {
		return copyarchive.RestoreTarget{}, pgerrors.ErrConflict
	}
	return copyarchive.RestoreTarget(r.plan.body.Target), nil
}

// Sensitive input for an encrypted descendant plan. Recovery supplies metadata;
// VerifyForWorker still requires the exact original target SQL transaction journal.
func (r Receipt) PrivateProofForSealing() ([]byte, error) {
	if r.validate() != nil {
		return nil, pgerrors.ErrConflict
	}
	raw, err := json.Marshal(workerProof{r.plan.body, r.body})
	if len(raw) > api.PostgresCopyEnvelopeMaxBytes {
		return nil, pgerrors.ErrQuotaExceeded
	}
	return raw, err
}

func RecoverReceiptForWorker(source copyinventory.ExportPlan, target copyarchive.RestoreTarget, raw []byte) (Receipt, error) {
	if len(raw) > api.PostgresCopyEnvelopeMaxBytes {
		return Receipt{}, pgerrors.ErrQuotaExceeded
	}
	var p workerProof
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil || d.Decode(new(any)) != io.EOF {
		return Receipt{}, pgerrors.ErrConflict
	}
	r := Receipt{Plan{p.Plan}, p.Receipt}
	if !r.MatchesSourceForWorker(source) || copyarchive.RestoreTarget(p.Plan.Target) != target {
		return Receipt{}, pgerrors.ErrConflict
	}
	return r, nil
}

// VerifyForWorker performs no role DDL or seed replay. The caller owns live
// dispatch/placement authorization and the surrounding shared session lock.
func (r Receipt) VerifyForWorker(ctx context.Context, conn *pgx.Conn) error {
	if r.validate() != nil {
		return pgerrors.ErrConflict
	}
	if err := authenticate(ctx, conn, copyarchive.RestoreTarget(r.plan.body.Target)); err != nil {
		return err
	}
	q := sqlc.New()
	private, err := q.PrivateRoleSeedReceipt(ctx, conn)
	if err != nil {
		return classify(ctx, err)
	}
	if !private {
		return pgerrors.ErrConflict
	}
	row, err := q.ReadRoleSeedWorkerProof(ctx, conn)
	if err != nil {
		return classify(ctx, err)
	}
	var p payload
	var ids []roleIdentity
	if json.Unmarshal(row.Plan, &p) != nil || json.Unmarshal(row.CreatedRoles, &ids) != nil || !reflect.DeepEqual(p, r.plan.body) || !reflect.DeepEqual(ids, r.body.Created) || !row.SeededAt.Valid || !row.SeededAt.Time.Equal(r.body.SeededAt) {
		return pgerrors.ErrConflict
	}
	if err = q.InstallRoleSeedAssertion(ctx, conn); err != nil {
		return classify(ctx, err)
	}
	raw, err := json.Marshal(r.expectedRoles())
	if err != nil {
		return pgerrors.ErrInvalid
	}
	_, err = q.AssertRoleSeedWorkerCatalogue(ctx, conn, raw)
	if err != nil {
		return classify(ctx, err)
	}
	return nil
}
