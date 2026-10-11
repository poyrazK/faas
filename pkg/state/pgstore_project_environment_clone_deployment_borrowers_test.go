//go:build !no_pg

// adr: 590
package state_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgCloneDeploymentBorrowers(t *testing.T) {
	for _, fault := range []string{"none", "stale", "unretired", "legacy", "cleaned"} {
		t.Run(fault, func(t *testing.T) {
			s, pool, l, _ := configurationCleanupFixture(t, fault != "legacy")
			native, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(t.Context(), l)
			if err != nil {
				t.Fatal(err)
			}
			if fault != "unretired" {
				retireConfigurationCleanupFixture(t, s, l)
			}
			if fault == "cleaned" {
				if err = s.CleanupProjectEnvironmentCloneConfigurationForLease(t.Context(), l); err != nil {
					t.Fatal(err)
				}
				if err = s.CleanupProjectEnvironmentCloneWorkloadsForLease(t.Context(), l); err != nil {
					t.Fatal(err)
				}
			}
			if fault == "stale" {
				l.Token = uuid.NewString()
			}
			report, err := s.ProjectEnvironmentCloneDeploymentBorrowersForLease(t.Context(), l)
			if fault == "stale" || fault == "unretired" || fault == "legacy" {
				if !errors.Is(err, state.ErrConflict) || !reflect.DeepEqual(report, state.ProjectEnvironmentCloneDeploymentBorrowers{}) {
					t.Fatal(report, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			id := native.Plan.Workloads[0].TargetDeploymentID
			counts := report.Deployments[id]
			if len(report.Deployments) != 1 || report.OperationID != l.Operation.ID || report.NativePlanSHA256 != state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlanSHA256(native.Plan) || len(report.NativeObservationSHA256) != 64 {
				t.Fatal(report)
			}
			// Schema changes must update the explicit SQL inventory, including CASCADE
			// and SET NULL references and both sides of lineage/mirror relationships.
			rows, err := pool.Query(t.Context(), "SELECT conname FROM pg_constraint WHERE contype='f' AND confrelid='deployments'::regclass")
			if err != nil {
				t.Fatal(err)
			}
			n := 0
			for rows.Next() {
				var name string
				if err = rows.Scan(&name); err != nil {
					t.Fatal(err)
				}
				if _, ok := counts[name]; !ok {
					t.Fatal("missing deployment borrower", name)
				}
				n++
			}
			rows.Close()
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
			if len(counts) != n {
				t.Fatal("stale borrower inventory", counts, n)
			}
			// Retained logs count even though deleting the deployment would cascade
			// them away. A source deployment's log must not enter the target count.
			beforeLogs := counts["deployment_logs_deployment_id_fkey"]
			for _, deploymentID := range []string{id, native.Plan.Workloads[0].SourceDeploymentID} {
				if _, err = pool.Exec(t.Context(), "INSERT INTO deployment_logs(deployment_id,seq,line) VALUES($1,987654321,'retained')", deploymentID); err != nil {
					t.Fatal(err)
				}
			}
			withLogs, err := s.ProjectEnvironmentCloneDeploymentBorrowersForLease(t.Context(), l)
			if err != nil || withLogs.Deployments[id]["deployment_logs_deployment_id_fkey"] != beforeLogs+1 {
				t.Fatal("retained or source references miscounted", withLogs, err)
			}
			original := counts["deployment_revision_pins_deployment_id_fkey"]
			counts["deployment_revision_pins_deployment_id_fkey"] = 999
			replay, err := s.ProjectEnvironmentCloneDeploymentBorrowersForLease(t.Context(), l)
			if err != nil || replay.Deployments[id]["deployment_revision_pins_deployment_id_fkey"] != original {
				t.Fatal("caller mutation changed inventory", replay, err)
			}
		})
	}
}
