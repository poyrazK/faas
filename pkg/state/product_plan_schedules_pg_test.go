//go:build !no_pg

package state

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/productcontrol"
)

func TestProductPlanSchedulesPostgres(t *testing.T) {
	s := newPgStore(t)
	productPlanSchedulesContract(t, s, func(t *testing.T, op *Operation, def OperationDefinition, adm OperationAdmission, now time.Time) error {
		ctx := t.Context()
		scope := ProductBillingScope{op.AccountID, op.AppID, op.PlatformTenantID, productcontrol.Period(now)}
		tx, err := s.productBillingLockedTx(ctx, scope)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := prepareProductOperationTx(ctx, tx, op, def, adm, now); err != nil {
			return err
		}
		return tx.Commit(ctx)
	})
}
