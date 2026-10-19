package datapipelines

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func TestLiveTableRemovalViewsRequireStableSelectionAndExactConfirmation(t *testing.T) {
	p := datapipeline.Pipeline{Initialized: true, DataPipeline: api.DataPipeline{ID: "pipeline", State: "live", DesiredState: "running", ObservationFresh: true, Tables: []string{"customers", "orders"}}}
	v := tableSelectionView(p, dashboard.DataPipelinesData{})
	if len(v.Removable) != 2 || v.CanRequest {
		t.Fatal("live removal required discovery", v)
	}
	p.Tables = []string{"orders"}
	if len(tableSelectionView(p, dashboard.DataPipelinesData{}).Removable) != 0 {
		t.Fatal("last table offered removal")
	}
	p.Tables = []string{"customers", "orders"}
	p.SchemaChange = &datapipeline.SchemaChange{State: "removal_requested", Supported: true, Cancellation: &datapipeline.TableCancellation{Kind: "live", Table: "customers", State: "requested"}}
	requested := schemaView(p)
	if !requested.TableRemoval || !requested.CanDiscardCancellation || requested.CanApproveCancellation {
		t.Fatal("unobserved removal cannot be dismissed safely", requested)
	}
	if selection := tableSelectionView(p, dashboard.DataPipelinesData{Check: &api.DataConnectionCheck{Fresh: true}}); selection.CanDiscard || selection.CanRequest || len(selection.Removable) > 0 {
		t.Fatal("other workflow bypassed removal", selection)
	}
	p.SchemaChange.Cancellation.State = "pending"
	p.State, p.Reason = "blocked", "table_removal_pending"
	if view := schemaView(p); !view.CanApproveCancellation || view.CanRetry || view.CanApprove || view.CanApproveRecovery {
		t.Fatal("live removal used wrong confirmation", view)
	}
	p.ObservationFresh = false
	if view := schemaView(p); view.CanApproveCancellation || !view.CanDiscardCancellation {
		t.Fatal("stale preview can be approved or cannot be dismissed", view)
	}
	p.ObservationFresh = true
	p.SchemaChange.Cancellation.State = "approved"
	if schemaView(p).CanDiscardCancellation {
		t.Fatal("approved live removal offered dismissal")
	}
}
