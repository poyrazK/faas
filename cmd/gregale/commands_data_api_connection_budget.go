package main

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func readDataAPIConnectionBudget(ctx context.Context, client *api.Client, app string) api.DataAPIConnectionBudgets {
	unknown := api.DataAPIConnectionBudgets{Version: 1, App: app, State: "unknown", Databases: []api.ManagedPostgresConnectionBudget{}}
	value, err := client.GetAppDataAPIConnectionBudgets(ctx, app)
	if err != nil || value.Version != 1 || value.App != app || len(value.Databases) > api.DataAPIConnectionBudgetMaxDatabases {
		return unknown
	}
	if value.State == "not_applicable" {
		if len(value.Databases) != 0 || value.Truncated {
			return unknown
		}
		return value
	}
	if len(value.Databases) == 0 || !slices.Contains([]string{"unknown", "within_budget", "over_budget"}, value.State) {
		return unknown
	}
	state := "within_budget"
	seen := map[string]bool{}
	for _, budget := range value.Databases {
		if seen[budget.DatabaseID] || !validDataAPIConnectionBudget(budget, time.Now()) {
			return unknown
		}
		seen[budget.DatabaseID] = true
		if budget.State == "over_budget" {
			state = "over_budget"
		} else if budget.State == "unknown" && state != "over_budget" {
			state = "unknown"
		}
	}
	if value.Truncated && state != "over_budget" {
		state = "unknown"
	}
	if value.State != state {
		return unknown
	}
	return value
}

func validDataAPIConnectionBudget(b api.ManagedPostgresConnectionBudget, now time.Time) bool {
	if _, err := uuid.Parse(b.DatabaseID); err != nil {
		return false
	}
	checked, first := time.Parse(time.RFC3339Nano, b.CheckedAt)
	expires, second := time.Parse(time.RFC3339Nano, b.ExpiresAt)
	if b.Version != 1 || !api.ValidAppSlug(b.DatabaseName) || b.Model != "canonical_runtime_pools" || b.Enforced || first != nil || second != nil ||
		now.Before(checked) || !now.Before(expires) || expires.Sub(checked) > api.DataAPIConnectionBudgetMaxAge {
		return false
	}
	for _, reason := range b.Reasons {
		if !validConnectionBudgetReason(reason) {
			return false
		}
	}
	i, d := b.Inventory, b.Demand
	for _, count := range []int64{i.Bindings, i.DataAPIConsumers, i.AuthConsumers, i.MigrationBindings, i.OtherBindings, i.ResidentInstances, i.ParkedInstances, i.AuxiliaryInstances, i.ActiveTasks, i.RotationPending, i.OverlapConsumers, i.StaleNodeInstances,
		d.DataAPIResidentEstimate, d.DataAPIWakeEstimate, d.AuthWakeEstimate, d.ControlHeadroom, d.MigrationHeadroom, d.ModeledPeakConnections} {
		if count < 0 || count > api.DataAPIConnectionBudgetMaxCount*api.DataAPIConnectionBudgetMaxBindings*api.AuthMaxPoolConnections {
			return false
		}
	}
	if d.ModeledPeakConnections != d.DataAPIWakeEstimate+d.AuthWakeEstimate+d.ControlHeadroom+d.MigrationHeadroom {
		return false
	}
	if b.Capacity.State == "unknown" {
		return b.Capacity.UsableConnections == nil && b.State == "unknown" && len(b.Reasons) > 0
	}
	if b.Capacity.State != "verified" || b.Capacity.UsableConnections == nil || *b.Capacity.UsableConnections < 1 || *b.Capacity.UsableConnections > api.DataAPIConnectionBudgetMaxCount {
		return false
	}
	if b.State == "unknown" {
		return len(b.Reasons) > 0
	}
	if len(b.Reasons) > 0 {
		return false
	}
	return b.State == "within_budget" && d.ModeledPeakConnections <= *b.Capacity.UsableConnections || b.State == "over_budget" && d.ModeledPeakConnections > *b.Capacity.UsableConnections
}

func validConnectionBudgetReason(reason string) bool {
	return slices.Contains([]string{"inventory_stale", "database_not_ready", "inventory_truncated", "runtime_inventory_unavailable", "runtime_pool_configuration_unverified", "external_consumers_unobserved",
		"migration_demand_unbounded", "client_pool_sizes_unknown", "capacity_unverified", "binding_transition", "inventory_invalid", "node_observations_stale", "auxiliary_connections_unmodeled", "multiple_runtime_bindings", "concurrency_policy_unverified"}, reason)
}

func attachDataAPIConnectionBudget(report *dataAPIHealthReport, budget api.DataAPIConnectionBudgets) {
	report.ConnectionBudget = &budget
	if budget.State == "unknown" || budget.Truncated {
		report.Partial = true
		report.PartialReasons = append(report.PartialReasons, "connection_budget_unknown")
	}
	if budget.State == "over_budget" {
		report.CurrentFindings = append(report.CurrentFindings, "connection_budget_exceeded")
		if report.Status == "healthy" {
			report.Status = "degraded"
		}
	}
}

func writeDataAPIConnectionBudget(budget *api.DataAPIConnectionBudgets) error {
	if budget == nil {
		return nil
	}
	_, err := fmt.Fprintf(osStdout, "Database connection budget: %s (planning only)\n", budget.State)
	if err != nil {
		return err
	}
	for _, database := range budget.Databases {
		capacity := "unknown"
		if database.Capacity.UsableConnections != nil {
			capacity = fmt.Sprint(*database.Capacity.UsableConnections)
		}
		if _, err := fmt.Fprintf(osStdout, "  %s: modeled peak %d; verified usable capacity %s; %s\n", database.DatabaseName, database.Demand.ModeledPeakConnections, capacity, database.State); err != nil {
			return err
		}
	}
	return nil
}
