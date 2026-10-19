//go:build !no_pg

package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestOrgResourceLifecyclePostgresHTTP(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	orgResourceLifecycleHTTP(t, *e.Store(), *e.Acct(), e.Do)
}
