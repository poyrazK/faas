//go:build !no_pg

package apidserver_test

// adr: 202610171908

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"testing"
)

func TestOrgQueuePostgresHTTPWorkerSurvivesCreatorErasure(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	orgQueueHTTPJourney(t, *e.S(), *e.Store(), *e.Acct(), e.Do)
}
