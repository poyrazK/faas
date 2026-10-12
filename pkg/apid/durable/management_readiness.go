package durable

import (
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"net/http"

	"github.com/onebox-faas/faas/pkg/state"
)

type DurableEntityReadinessCheck struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type DurableEntityReadiness struct {
	SchemaVersion      int                           `json:"schema_version"`
	Scope              string                        `json:"scope"`
	ConfigurationReady bool                          `json:"configuration_ready"`
	ReleaseReady       bool                          `json:"release_ready"`
	Checks             []DurableEntityReadinessCheck `json:"checks"`
}

// This snapshot deliberately does not probe storage, invoke guests, consume
// admission budgets, or infer fleet acceptance from this node's startup.
func (s *Service) DurableEntityReadinessForApp(appID string) DurableEntityReadiness {
	report := DurableEntityReadiness{SchemaVersion: 1, Scope: "serving_node", ConfigurationReady: true}
	configuration := func(id string, enabled bool, required bool, detail string) {
		status := "configured"
		if !enabled {
			status = "disabled"
			if required {
				report.ConfigurationReady = false
			}
		}
		report.Checks = append(report.Checks, DurableEntityReadinessCheck{id, status, detail})
	}
	configuration("conditional_storage", s.Engine != nil, true, "Configured manager passed startup checks; current bucket health and fleet consistency are not checked here.")
	configuration("app_allowlist", s.Apps[appID], true, "This app must be explicitly enabled on the serving node.")
	configuration("tenant_quotas", s.TenantQuotas != nil, true, "Release baseline requires shared tenant admission budgets and a positive per-entity retained-byte cap.")
	configuration("quota_migration_workflow", s.QuotaMigrationEnabled, false, "Optional fenced reservation backfill and coverage scan; scope completion is inspected separately.")
	configuration("quota_policy_workflow", s.QuotaPolicyEnabled, false, "Optional audited quota increases; persisted policy and node alignment are inspected separately.")
	configuration("retirement_workflow", s.RetirementEnabled, false, "Optional permanent object retirement and verified capacity reclamation; native acceptance remains external.")
	configuration("service_sessions", s.Sockets != nil, false, "Optional bounded WebSocket transport.")
	configuration("browser_sessions", s.SessionGrants != nil, false, "Optional scoped browser session grants.")
	configuration("alarms", s.AlarmsEnabled, false, "Optional scheduled entity wake-ups.")
	configuration("health_inspection", s.HealthEnabled, false, "Optional entity health discovery.")
	configuration("outbox", s.OutboxEnabled, false, "Optional durable outgoing-intent relay.")
	configuration("outbox_handlers", s.OutboxHandlersEnabled, false, "Optional invocation-produced outgoing intents.")
	configuration("backups", s.BackupsEnabled, false, "Optional bucket-backed backups.")
	configuration("maintenance", s.MaintenanceEnabled, false, "Optional bounded retained-history maintenance.")
	configuration("isolated_restore", s.RestoreValidationEnabled && s.RestoreIsolationEnabled && s.executionAPIEnabled(), false, "Optional disposable application restore validation.")
	configuration("validator_release_gate", s.ValidatorReleaseGateEnabled, false, "Optional validator binding checks before increasing deployment traffic.")
	report.Checks = append(report.Checks, []DurableEntityReadinessCheck{
		{"source_ci", "unverified", "Review green CI for the exact candidate source commit; this endpoint has no trusted CI evidence."},
		{"native_acceptance", "unverified", "Require independent private-bucket/KVM failure, takeover, restart and multi-node acceptance for the candidate and deployment configuration."},
		{"quota_migration", "unverified", "Inventory and backfill existing entity reservations, or establish a fresh scope, before quota enablement."},
		{"fleet_configuration", "unverified", "Verify consistent quota mode, backend placement, bucket, limits and clocks across serving nodes; drain older connections."},
	}...)
	return report
}

func (s *Service) InspectDurableEntityReadiness(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusOK, s.DurableEntityReadinessForApp(app.ID))
}
