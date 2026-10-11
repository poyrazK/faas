//go:build !no_pg

package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestObjectCustomerRequestAccountingPG(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	objectCustomerRequestAccounting(t, e.s, e.store, e.acct, e.key)
}
