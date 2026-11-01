// adr: 20261028103115 — production managed PostgreSQL provisioning follows the
// managed-postgres capability flag; a paused fleet answers its own code.
// adr: 20261031091607 — while the caps are not on, only a flag rule releases.
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
	"github.com/onebox-faas/faas/pkg/platformflags"
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
	// managed-postgres is GA (ADR-20261031091607): released by default, and
	// the GA rollout closes it with a stored definition until the owner's
	// production test.
	if !srv.ManagedPostgresReleased(ctx, account) {
		t.Fatal("a GA capability is released by default")
	}
	srv.SeedPlatformFlag(capabilityFlag(t, "managed-postgres"), dataAPITestHidden)
	if srv.ManagedPostgresReleased(ctx, account) {
		t.Fatal("a stored default-off definition must close the account")
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

func TestManagedPostgresUncappedReleaseNeedsAFlagRule(t *testing.T) {
	ctx := context.Background()
	e := apidserver.Setup(t, api.PlanPro)
	srv := *e.S()
	account := (*e.Acct()).ID
	if err := srv.InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}
	srv.SetManagedPostgresUncapped(true)
	if srv.ManagedPostgresReleased(ctx, account) {
		t.Fatal("while the caps are not on, the GA default must not release an account")
	}
	on := true
	srv.SeedPlatformFlag(capabilityFlag(t, "managed-postgres"), platformflags.Definition{Enabled: true, Default: true,
		Rules: []platformflags.Rule{{ID: "owner", Accounts: []string{account}, Value: &on}}})
	if !srv.ManagedPostgresReleased(ctx, account) {
		t.Fatal("an account a flag rule names is released while the caps are not on")
	}
	srv.SetManagedPostgresUncapped(false)
	srv.SeedPlatformFlag(capabilityFlag(t, "managed-postgres"), dataAPITestReleased)
	if !srv.ManagedPostgresReleased(ctx, account) {
		t.Fatal("with the caps on, the default releases the account")
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
