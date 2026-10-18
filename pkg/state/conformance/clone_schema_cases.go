// adr: 590
package conformance

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// Readiness policy and receiver credentials need an isolated copy strategy;
// probe leases remain operational state of the source. Incident evidence is
// retained customer data since #4882, so it needs an isolated data strategy
// and must never become copied configuration.
func testMCPReadinessCloneInventory(t *testing.T, fx *Fixture) {
	tables, err := state.ProjectEnvironmentCloneSchemaPolicies()
	if err != nil {
		t.Fatal(err)
	}
	var blockers []state.ProjectEnvironmentCloneCoverageBlocker
	inventory, catalog := fx.Store.(state.ProjectEnvironmentCloneSchemaCoverageStore)
	if catalog {
		project, err := fx.Store.CreateProject(fx.Ctx, state.Project{AccountID: fx.Account.ID, Slug: "readiness-inventory"})
		if err != nil {
			t.Fatal(err)
		}
		coverage, err := inventory.ProjectEnvironmentCloneSchemaCoverage(fx.Ctx, fx.Account.ID, project.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !coverage.Known {
			t.Fatalf("readiness migration inventory is incomplete: %+v", coverage.Blockers)
		}
		tables, blockers = coverage.Tables, coverage.Blockers
	}
	for _, table := range []string{"app_mcp_readiness_policies", "mcp_readiness_pagerduty_acknowledgements"} {
		index := slices.IndexFunc(tables, func(p state.ProjectEnvironmentCloneSchemaPolicy) bool { return p.TableName == table })
		if index < 0 || tables[index].Kind != state.CloneSchemaConfiguration {
			t.Fatalf("readiness policy or receiver credentials lost their configuration boundary: %s", table)
		}
		if catalog && !slices.ContainsFunc(blockers, func(b state.ProjectEnvironmentCloneCoverageBlocker) bool {
			return b.Table == table && b.Code == "isolated_strategy_unavailable"
		}) {
			t.Fatalf("configuration capture must still require isolation: %s", table)
		}
	}
	for _, table := range []string{"deployment_mcp_readiness_monitors"} {
		index := slices.IndexFunc(tables, func(p state.ProjectEnvironmentCloneSchemaPolicy) bool { return p.TableName == table })
		if index < 0 || tables[index].Kind != state.CloneSchemaOperational {
			t.Fatalf("readiness probe leases became copied configuration: %s", table)
		}
		if slices.ContainsFunc(blockers, func(b state.ProjectEnvironmentCloneCoverageBlocker) bool { return b.Table == table }) {
			t.Fatalf("operational readiness state requires no copy strategy: %s", table)
		}
	}
	for _, table := range []string{"mcp_readiness_incident_events"} {
		index := slices.IndexFunc(tables, func(p state.ProjectEnvironmentCloneSchemaPolicy) bool { return p.TableName == table })
		if index < 0 || tables[index].Kind != state.CloneSchemaData {
			t.Fatalf("readiness evidence lost its retained-data boundary: %s", table)
		}
		if catalog && !slices.ContainsFunc(blockers, func(b state.ProjectEnvironmentCloneCoverageBlocker) bool {
			return b.Table == table && b.Code == "isolated_data_strategy_unavailable"
		}) {
			t.Fatalf("retained readiness evidence must still require an isolated data strategy: %s", table)
		}
	}
}
