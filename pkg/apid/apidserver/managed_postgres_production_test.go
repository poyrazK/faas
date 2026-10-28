// adr: 20261028103115 — production managed PostgreSQL provisioning follows the
// managed-postgres capability flag; a paused fleet answers its own code.
package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestManagedPostgresProductionReleaseFollowsTheCapabilityFlag(t *testing.T) {
	ctx := context.Background()
	e := apidserver.Setup(t, api.PlanPro)
	srv := *e.S()
	account := (*e.Acct()).ID
	if !srv.ManagedPostgresReleased(ctx, account) {
		t.Fatal("without a capability gate every account is released")
	}
	if err := srv.InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}
	if srv.ManagedPostgresReleased(ctx, account) {
		t.Fatal("a preview capability is hidden by default")
	}
	srv.SeedPlatformFlag(capabilityFlag(t, "managed-postgres"), dataAPITestReleased)
	if !srv.ManagedPostgresReleased(ctx, account) {
		t.Fatal("the released flag must release the account")
	}
	if srv.ManagedPostgresReleased(ctx, uuid.NewString()) {
		t.Fatal("an account that cannot be read is not released")
	}
	srv.SeedPlatformFlag(capabilityFlag(t, "managed-postgres"), dataAPITestHidden)
	if srv.ManagedPostgresReleased(ctx, account) {
		t.Fatal("the kill switch must close the account again")
	}
}

func TestManagedPostgresFleetPausedAnswersItsOwnProblem(t *testing.T) {
	w := httptest.NewRecorder()
	apidserver.ManagedPostgresProblem(w, managedpostgres.ErrFleetPaused)
	var problem api.Problem
	if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusServiceUnavailable || problem.Code != "managed_postgres_new_databases_paused" || problem.Detail != managedpostgres.FleetPausedDetail {
		t.Fatalf("fleet paused = %d %+v", w.Code, problem)
	}
}
