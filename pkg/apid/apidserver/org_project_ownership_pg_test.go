//go:build !no_pg

package apidserver_test

// adr: 202610171905

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"testing"
)

func TestOrgProjectOwnershipPostgresHTTP(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	orgProjectHTTP(t, *e.Store(), *e.Acct(), e.Do)
}

func TestOrgProjectSourceDeploymentPostgresHTTP(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	orgProjectSourceHTTP(t, *e.Store(), *e.Acct(), *e.H())
}
