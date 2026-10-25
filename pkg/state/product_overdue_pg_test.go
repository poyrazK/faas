//go:build !no_pg

package state

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261018212918
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
