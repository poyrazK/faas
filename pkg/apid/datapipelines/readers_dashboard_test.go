package datapipelines

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func TestReaderViewShowsExactDiffDuringQuarantine(t *testing.T) {
	p := datapipeline.Pipeline{Initialized: true, DataPipeline: api.DataPipeline{ID: "pipeline", Name: "orders", Revision: 4, Generation: 1, DesiredState: "running", State: "blocked", Reason: "live_table_mismatch", Tables: []string{"customers", "orders"}, ReadSubjects: []string{"alice", "unchanged"}}, SchemaChange: &datapipeline.SchemaChange{State: "verifying", Recovery: &datapipeline.TableRecovery{Kind: "live", State: "detected"}}}
	if !readersView(p).CanRequest {
		t.Fatal("quarantine blocked reader changes")
	}
	p.ReaderChange = &datapipeline.ReaderChange{ID: "exact-preview", State: "preview", PreviewRevision: 4, Generation: 1, Before: p.ReadSubjects, After: []string{"bob", "unchanged"}, Tables: p.Tables}
	v := readersView(p)
	if !v.CanApprove || !v.CanDiscard || v.CanRequest || !slices.Equal(v.Added, []string{"bob"}) || !slices.Equal(v.Removed, []string{"alice"}) || len(v.Tables) != 2 {
		t.Fatal("incorrect reader preview", v)
	}
	p.Revision++
	if v = readersView(p); v.CanApprove || !v.CanDiscard {
		t.Fatal("stale preview offered approval", v)
	}
	p.ReaderChange.State = "approved"
	if v = readersView(p); v.CanApprove || v.CanDiscard || v.CanRequest {
		t.Fatal("pending application offered replacement", v)
	}
	p.SchemaChange = &datapipeline.SchemaChange{ID: "schema", State: "pending", Supported: true}
	p.ObservationFresh = true
	if schemaView(p).CanApprove {
		t.Fatal("schema approval offered before reader acknowledgement")
	}
	p.SchemaChange = nil
	p.State = "live"
	selection := tableSelectionView(p, dashboard.DataPipelinesData{Check: &api.DataConnectionCheck{Fresh: true, Tables: []api.DataSourceTable{{Name: "new_table", Supported: true}}}})
	if selection.CanRequest {
		t.Fatal("table request offered before reader acknowledgement")
	}
	p.ReaderChange.State = "applied"
	if !readersView(p).CanRequest {
		t.Fatal("applied change blocked next edit")
	}
}
