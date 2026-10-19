//go:build !no_pg

package apidserver_test

// adr: 202610171907

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestOrgObjectStoragePostgresHTTPAndS3(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	orgObjectStorageJourney(t, *e.S(), *e.Store(), *e.Acct(), *e.Key())
}
