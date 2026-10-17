// ADR-1580 / ADR-1576: recovery authority is operational clone inventory.
package ciapi_test

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestCIAutomationRecoveryCloneSchemaCoverage(t *testing.T) {
	f := newAutomationRecoveryAPI(t)
	store := state.NewPgStore(f.pool)
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: f.account, Slug: "ci-recovery-coverage"})
	if err != nil {
		t.Fatal(err)
	}
	report, err := store.ProjectEnvironmentCloneSchemaCoverage(t.Context(), f.account, project.ID)
	if err != nil || !report.Known || len(report.Hash) != 64 {
		t.Fatalf("new migration is missing clone inventory: %+v %v", report.Blockers, err)
	}
	seen := 0
	for _, table := range report.Tables {
		switch table.TableName {
		case "ci_automation_events":
			if table.Kind != state.CloneSchemaOperational || !slices.Contains(table.Columns, "retry_generation") || !slices.Contains(table.Columns, "superseded_by") {
				t.Fatal("source recovery generation became unregistered or cloneable configuration")
			}
			seen++
		case "ci_automation_branches":
			if table.Kind != state.CloneSchemaOperational || !slices.Contains(table.Columns, "head_fence") || len(table.Columns) != 4 {
				t.Fatal("branch observation fences became cloneable configuration")
			}
			seen++
		case "ci_automation_event_retries":
			if table.Kind != state.CloneSchemaOperational || len(table.Columns) != 8 {
				t.Fatal("recovery journal became unregistered or cloneable configuration")
			}
			seen++
		}
	}
	if seen != 3 {
		t.Fatal("migrated recovery tables were omitted from production schema coverage")
	}
	if _, err := f.pool.Exec(t.Context(), "ALTER TABLE ci_automation_event_retries ADD COLUMN unexpected_ci_recovery text"); err != nil {
		t.Fatal(err)
	}
	report, err = store.ProjectEnvironmentCloneSchemaCoverage(t.Context(), f.account, project.ID)
	want := state.ProjectEnvironmentCloneCoverageBlocker{Table: "ci_automation_event_retries", Column: "unexpected_ci_recovery", Code: "unregistered_column"}
	if err != nil || report.Known || !slices.Contains(report.Blockers, want) {
		t.Fatalf("registry update bypassed unknown-schema protection: %+v %v", report.Blockers, err)
	}
}
