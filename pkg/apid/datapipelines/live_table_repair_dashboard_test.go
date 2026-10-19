package datapipelines

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func TestLiveTableRepairViewKeepsRepairConsentSeparate(t *testing.T) {
	p := datapipeline.Pipeline{DataPipeline: api.DataPipeline{ID: "pipeline", Name: "orders", State: "blocked", Reason: "live_table_mismatch", DesiredState: "running", ObservationFresh: true, Tables: []string{"customers", "orders"}}, SchemaApproval: "repair"}
	p.SchemaChange = &datapipeline.SchemaChange{ID: "repair", State: "verifying", Supported: true, RequestedTables: p.Tables, Recovery: &datapipeline.TableRecovery{Kind: "live", Table: "customers", State: "detected", Stage: "quarantined"}}
	p.VerificationProgress = &datapipeline.VerificationProgress{ID: "mismatch", State: "mismatch", Table: "customers"}
	v := schemaView(p)
	if !v.CanRecover || len(v.RecoveryChoices) != 1 || v.RecoveryChoices[0] != "customers" || v.CanApprove || v.CanRetry || v.TableAddition || len(v.CancellationChoices) != 0 {
		t.Fatal("live repair exposed another approval/action", v)
	}
	p.Reason = "table_recovery_pending"
	p.SchemaChange.Recovery.State, p.SchemaChange.Recovery.Stage = "pending", "preview"
	v = schemaView(p)
	if !v.CanApproveRecovery || v.CanRetry || v.CanApprove {
		t.Fatal("exact repair confirmation missing", v)
	}
	p.ObservationFresh = false
	if schemaView(p).CanApproveRecovery {
		t.Fatal("stale preview can be approved")
	}
}
