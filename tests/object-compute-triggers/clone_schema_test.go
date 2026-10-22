package computetriggers_test

import (
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// The inventory authenticates schema coverage. It must keep configuration
// isolated-strategy blockers until frozen capture and promotion are supported.
func TestStorageComputeCloneSchemaPG(t *testing.T) {
	st, ctx := pgStore(t)
	account, err := st.CreateAccount(ctx, "clone-storage-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := st.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "storage"})
	if err != nil {
		t.Fatal(err)
	}
	report, err := st.ProjectEnvironmentCloneSchemaCoverage(ctx, account.ID, project.ID)
	if err != nil || !report.Known {
		t.Fatalf("migrated storage schema is unregistered: %+v, %v", report, err)
	}
	for table, column := range map[string]string{
		"app_binding_release_policies": "object_storage_requirements",
		"object_bucket_notifications":  "managed_rules",
	} {
		registered, blocked := false, false
		for _, policy := range report.Tables {
			if policy.TableName == table && policy.Kind == state.CloneSchemaConfiguration && slices.Contains(policy.Columns, column) {
				registered = true
			}
		}
		for _, blocker := range report.Blockers {
			if blocker.Table == table && blocker.Code == "isolated_strategy_unavailable" {
				blocked = true
			}
		}
		if !registered || !blocked {
			t.Fatalf("%s.%s registered=%t, isolated=%t", table, column, registered, blocked)
		}
	}
}
