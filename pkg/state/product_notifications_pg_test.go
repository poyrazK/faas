//go:build !no_pg

package state

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProductControlNotificationsPostgres(t *testing.T) {
	s := newPgStore(t)
	settle := func(op Operation) {
		t.Helper()
		tx, err := s.pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(t.Context()) }()
		if err = settleProductOperationTx(t.Context(), tx, op); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	productNotificationContract(t, s, settle)
}

func TestProductControlNotificationBoundsPostgres(t *testing.T) {
	s := newPgStore(t)
	seed := func(scope ProductNotificationScope, expires time.Time) {
		t.Helper()
		if _, err := s.pool.Exec(t.Context(), `DELETE FROM product_control_notification_reads WHERE account_id=$1`, scope.AccountID); err != nil {
			t.Fatal(err)
		}
		_, err := s.pool.Exec(t.Context(), `INSERT INTO product_control_notification_reads(account_id,app_id,tenant_id,auth_app_id,auth_organization_id,source_kind,source_id,source_version,is_read,version,updated_at,expires_at)
SELECT $1,$2,$3,$4,$5,'help',gen_random_uuid(),'help:2',true,1,$6::timestamptz-interval '1 hour',$6 FROM generate_series(1,$7::int)
`, scope.AccountID, scope.AppID, scope.TenantID, scope.AuthAppID, scope.OrganizationID, expires, api.ProductControlNotificationMaxReads)
		if err != nil {
			t.Fatal(err)
		}
	}
	productNotificationBounds(t, s, seed)
	f := helpFixture(t.Context(), t, s)
	scope := ProductNotificationScope{f.scope}
	scope.Role = "operator"
	f.admission.IdempotencyKey = "notification-attention"
	op, _, err := s.AdmitOperation(t.Context(), f.admission)
	if err != nil {
		t.Fatal(err)
	}
	op.State, op.UpdatedAt = api.OperationRequiresReconciliation, time.Now().UTC().Truncate(time.Microsecond)
	record, err := json.Marshal(op)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(t.Context(), `UPDATE customer_operations SET state=$2,record=$3 WHERE id=$1`, op.ID, op.State, record); err != nil {
		t.Fatal(err)
	}
	productNotificationAttention(t, s, scope, op)
}
