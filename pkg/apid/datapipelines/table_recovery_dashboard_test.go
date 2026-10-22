package datapipelines

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func TestTableRecoveryViewRestrictsApprovalAndRetry(t *testing.T) {
	id := strings.Repeat("a", 64)
	p := datapipeline.Pipeline{DataPipeline: api.DataPipeline{State: "blocked", DesiredState: "running", Reason: "replica_mismatch", ObservationFresh: true}, SchemaApproval: id,
		SchemaChange:         &datapipeline.SchemaChange{ID: id, State: "verifying", Supported: true, AddedTables: []string{"customers"}},
		VerificationProgress: &datapipeline.VerificationProgress{Table: "customers", State: "mismatch"}}
	if v := schemaView(p); !v.CanRecover || v.RecoveryTable != "customers" || v.CanApproveRecovery {
		t.Fatal("failed new table recovery missing", v)
	}
	p.VerificationProgress.Table = "orders"
	if schemaView(p).CanRecover {
		t.Fatal("healthy baseline table offered recovery")
	}
	p.VerificationProgress.Table = "customers"
	p.SchemaChange.Recovery = &datapipeline.TableRecovery{Table: "customers", ID: strings.Repeat("b", 64), State: "pending", Stage: "preview"}
	p.Reason = "table_recovery_pending"
	if v := schemaView(p); !v.CanApproveRecovery || v.CanRetry || v.CanRecover {
		t.Fatal("preview did not require exact approval", v)
	}
	p.ObservationFresh = false
	if schemaView(p).CanApproveRecovery {
		t.Fatal("stale recovery approval offered")
	}
	p.ObservationFresh = true
	p.SchemaChange.Recovery.State = "approved"
	p.SchemaChange.Recovery.Stage = "cleared"
	p.Reason = "table_recovery_copying"
	if schemaView(p).CanRetry {
		t.Fatal("schema retry offered during destructive recovery")
	}
	p.SchemaChange.Recovery.Stage = "verifying"
	p.Reason = "schema_api_refresh_failed"
	if !schemaView(p).CanRetry {
		t.Fatal("failed API refresh cannot retry after copy")
	}
	p.SchemaChange.Recovery = nil
	p.VerificationProgress = nil
	p.Reason = "table_copy_stalled"
	p.SchemaChange.Copies = []datapipeline.TableCopyProgress{{Table: "customers", Phase: "copying", NativeState: "d", Stalled: true, Rows: 1200, Bytes: 32000, CountersKnown: true}}
	if v := schemaView(p); !v.CanRecover || len(v.RecoveryChoices) != 1 || v.RecoveryChoices[0] != "customers" || len(v.Copies) != 1 || v.Copies[0].Rows != 1200 {
		t.Fatal("stalled COPY progress/selection missing", v)
	}
	p.SchemaChange.Copies[0].NativeState = "r"
	if schemaView(p).CanRecover {
		t.Fatal("ready COPY offered destructive recovery")
	}
}
