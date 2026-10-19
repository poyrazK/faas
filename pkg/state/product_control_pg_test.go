//go:build !no_pg

package state

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProductControlPostgres(t *testing.T) { productControlContract(t, newPgStore(t)) }

func TestProductControlDeploymentPilotPostgres(t *testing.T) {
	s := newPgStore(t)
	result := productDeploymentPilot(t, s)
	if _, err := s.pool.Exec(t.Context(), "DELETE FROM customer_operations WHERE id=$1", result.operation.ID); err != nil {
		t.Fatal(err)
	}
	checkProductRetainedReceipt(t, s, result)
}

func TestProductOverdueHoldsPostgres(t *testing.T) {
	s := newPgStore(t)
	productOverdueContract(t, s, func(account, app string, doc api.ProductBillingDocument) {
		tx, err := s.pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(t.Context()) }()
		scope := ProductBillingScope{AccountID: account, AppID: app, TenantID: doc.TenantID, Period: doc.Period}
		if err := productBillingPGInsert(t.Context(), tx, scope, doc); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}
