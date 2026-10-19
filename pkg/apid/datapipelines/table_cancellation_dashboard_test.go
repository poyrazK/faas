package datapipelines

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func TestTableCancellationViewFencesOtherApprovals(t *testing.T) {
	var plan datapipeline.SchemaChange
	if err := json.Unmarshal([]byte(`{"ID":"change","State":"verifying","Supported":true,"Before":[{"Name":"orders"}],"AddedTables":["customers"]}`), &plan); err != nil {
		t.Fatal(err)
	}
	p := datapipeline.Pipeline{SchemaChange: &plan, SchemaApproval: plan.ID, DataPipeline: api.DataPipeline{DesiredState: "running", State: "blocked", ObservationFresh: true, Reason: "schema_api_refresh_failed"}}
	if v := schemaView(p); len(v.CancellationChoices) != 1 || v.CancellationChoices[0] != "customers" {
		t.Fatal("approved addition cannot cancel", v)
	}
	plan.Cancellation = &datapipeline.TableCancellation{ID: "cancel", Table: "customers", State: "pending", Stage: "preview", Retention: "retain_private"}
	p.Reason = "table_cancellation_pending"
	if v := schemaView(p); !v.CanApproveCancellation || !v.CanDiscardCancellation || v.CanRecover || v.CanRetry || v.CanApproveRecovery {
		t.Fatal("cancellation bypassed another approval", v)
	}
	p.ObservationFresh = false
	if schemaView(p).CanApproveCancellation {
		t.Fatal("stale cancellation offered")
	}
	p.ObservationFresh = true
	plan.Cancellation.State = "approved"
	if v := schemaView(p); v.CanApproveCancellation || v.CanDiscardCancellation || len(v.CancellationChoices) > 0 {
		t.Fatal("active cleanup can be replaced", v)
	}
}
