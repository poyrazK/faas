//go:build !no_pg

package state

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProductOwnerBillingQueuePostgres(t *testing.T) {
	s := newPgStore(t)
	ctx := t.Context()
	insert, settle := productBillingPGWriters(t, s)
	productOwnerBillingQueueContract(t, s, productOwnerBillingQueueFixture{
		assignment: func(scope ProductBillingScope, at time.Time) {
			_, err := s.pool.Exec(ctx, `UPDATE product_control_plan_history SET effective_at=$4
WHERE account_id=$1 AND app_id=$2 AND tenant_id=$3`, scope.AccountID, scope.AppID, scope.TenantID, at)
			if err != nil {
				t.Fatal(err)
			}
		},
		schedule: func(receipt api.ProductCustomerChangeReceipt, at time.Time) {
			_, err := s.pool.Exec(ctx, `UPDATE product_control_customer_changes
SET scheduled_for=$2, receipt=jsonb_set(receipt, '{effective_at}', to_jsonb($2::timestamptz))
WHERE id=$1 AND schedule_status='pending'`, receipt.ID, at)
			if err != nil {
				t.Fatal(err)
			}
		},
		trial: func(scope ProductBillingScope, start, end time.Time) {
			_, err := s.pool.Exec(ctx, `UPDATE product_control_customers SET trial_starts_at=$3, trial_ends_at=$4
WHERE app_id=$1 AND tenant_id=$2`, scope.AppID, scope.TenantID, start, end)
			if err != nil {
				t.Fatal(err)
			}
		},
		receipt: func(op Operation) {
			insert(op)
			settle(op)
		},
	})
}
