package migrationverify

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMigrationConnectionEvidencePinsTargetsAndRequiresCompleteGuestReport(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "old guest omits check"}[missing], func(t *testing.T) {
			b, m, _, in := fixture(t)
			in.ColdWake = false
			in.Checks = api.MigrationDependencyChecks{ServiceConnections: []api.MigrationServiceConnectionCheck{{EnvKey: "API_URL", Service: "api", DeploymentID: uuid.NewString()}}}
			current := true
			b.ConnectionsCurrent = func(context.Context, []api.MigrationServiceConnectionCheck) error {
				if !current {
					return state.ErrConflict
				}
				return nil
			}
			run, err := b.Create(t.Context(), in)
			if err != nil {
				t.Fatal(err)
			}
			run = advance(t, b, run.ID)
			task, err := m.AppTaskByID(t.Context(), b.AccountID, b.AppID, run.DependencyTaskID)
			if err != nil || task.DeploymentID != run.DeploymentID {
				t.Fatal("caller deployment not pinned")
			}
			checks, ok := api.ParseMigrationDependenciesCommand(task.Command, task.CommandShell)
			if !ok || len(checks.ServiceConnections) != 1 || checks.ServiceConnections[0] != in.Checks.ServiceConnections[0] {
				t.Fatal("target selector lost in guest task")
			}
			output := ""
			if missing {
				raw, _ := json.Marshal(api.MigrationDependencyReport{Checks: []api.MigrationVerificationCheck{}})
				output = string(raw)
			}
			completeDependency(t, m, "service/API_URL", state.AppTaskSucceeded, output)
			run = advance(t, b, run.ID)
			if missing {
				if run.Status != "failed" {
					t.Fatal("incomplete report passed")
				}
				return
			}
			run = advance(t, b, run.ID)
			if run.Status != "passed" || !strings.Contains(run.Checks[0].Detail, "selected deployment") || strings.Contains(run.Checks[0].Detail, "PRIVATE_URL") {
				t.Fatalf("evidence: %+v", run)
			}
			current = false
			blocked, err := b.Get(t.Context(), run.ID)
			if err != nil || blocked.Status != "blocked" || blocked.Reason != "service_connection_target_changed" {
				t.Fatalf("stale target evidence accepted: %+v %v", blocked, err)
			}
		})
	}
}

func TestMigrationConnectionsFailClosedWithoutTargetValidation(t *testing.T) {
	b, _, _, in := fixture(t)
	in.Checks.ServiceConnections = []api.MigrationServiceConnectionCheck{{EnvKey: "API_URL", Service: "api", DeploymentID: uuid.NewString()}}
	if _, err := b.Create(t.Context(), in); err == nil {
		t.Fatal("missing target validator accepted")
	}
	b.ConnectionsCurrent = func(context.Context, []api.MigrationServiceConnectionCheck) error { return state.ErrConflict }
	if _, err := b.Create(t.Context(), in); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("invalid target accepted: %v", err)
	}
}
