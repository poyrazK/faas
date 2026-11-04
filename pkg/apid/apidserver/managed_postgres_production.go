// adr: 20261028103115 — production managed PostgreSQL provisioning.
package apidserver

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

// managedPostgresCapability is the capability whose flag releases production
// provisioning to an account.
const managedPostgresCapability = "managed-postgres"

// managedPostgresReleased decides production provisioning for one account by
// the managed-postgres capability flag, for the account's plan and the
// request's organization. Without a gate (unit tests) every account is
// released; an account that cannot be read is not.
func (s *server) managedPostgresReleased(ctx context.Context, accountID string) bool {
	if s.capGate == nil {
		return true
	}
	acct, err := s.store.AccountByID(ctx, accountID)
	if err != nil {
		return false
	}
	return s.capGate.Enabled(managedPostgresCapability, platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan), OrgID: managedpostgres.Organization(ctx)})
}

// logManagedPostgresProductionReadiness reports at startup why production
// provisioning is closed, and records the approval's end for the expiry
// alerts. Reasons are stable codes; no secret reaches the log.
func logManagedPostgresProductionReadiness(registry *managedpostgres.Registry, getenv func(string) string, metrics *managedpostgres.Metrics, log *slog.Logger) {
	approval := managedpostgres.LoadProductionApproval(getenv)
	metrics.SetProductionApprovalUntil(approval.Until)
	readiness := registry.ProductionReadiness(approval, time.Now().UTC())
	if log == nil {
		return
	}
	if readiness.Ready {
		log.Info("managed postgres production provisioning approved", "backend_id", approval.BackendID, "until", approval.Until)
		return
	}
	log.Warn("managed postgres production provisioning is closed", "reasons", readiness.Reasons)
}

// observeManagedPostgresFleet records fleet totals after each usage sweep, so
// the fleet alerts follow the ledger the spend breaker reads.
func observeManagedPostgresFleet(ctx context.Context, service *managedpostgres.Service, metrics *managedpostgres.Metrics, log *slog.Logger) {
	if service == nil || metrics == nil {
		return
	}
	totals, policy, err := service.FleetStatus(ctx)
	if err != nil {
		if ctx.Err() == nil && log != nil {
			log.Warn("managed postgres fleet totals unavailable", "error", err)
		}
		return
	}
	metrics.ObserveFleet(totals, policy)
}
