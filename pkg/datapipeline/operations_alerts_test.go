package datapipeline

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestQualityIncidentDoesNotRecoverFromUnknownOrIncompleteScan(t *testing.T) {
	_, store, p, w := alertFixture(t)
	alertLive(t, store, p)
	c := &api.DataPipelineOperations{Version: 1, Quality: &api.DataPipelineQualitySchedule{EverySeconds: 3600, Rules: []api.DataPipelineQualityRule{{Name: "id_required", Kind: "required", Table: "orders", Columns: []string{"id"}}}}}
	status := &api.DataPipelineOperationsStatus{Quality: &api.DataPipelineQualityStatus{State: "failed", Generation: p.Generation, CheckedAt: time.Now().UTC().Format(time.RFC3339Nano)}}
	if _, err := store.pool.Exec(t.Context(), `UPDATE data_pipelines SET work_config=$2,work_status=$3 WHERE id=$1`, p.ID, operationsJSON(c), operationsJSON(status)); err != nil {
		t.Fatal(err)
	}
	alertDue(t, t.Context(), w, p, false)
	alertDue(t, t.Context(), w, p, true)
	history := alertHistory(t, t.Context(), store, p)
	if len(history) != 1 || history[0].Code != "quality_failed" || history[0].Kind != "incident" {
		t.Fatal("missing quality incident", history)
	}
	for _, state := range []string{"unknown", "inconclusive"} {
		status.Quality.State = state
		if _, err := store.pool.Exec(t.Context(), `UPDATE data_pipelines SET work_status=$2 WHERE id=$1`, p.ID, operationsJSON(status)); err != nil {
			t.Fatal(err)
		}
		alertDue(t, t.Context(), w, p, false)
		alertDue(t, t.Context(), w, p, true)
		if len(alertHistory(t, t.Context(), store, p)) != 1 {
			t.Fatal("unconfirmed quality cleared failure", state)
		}
	}
	status.Quality.State = "passed"
	if _, err := store.pool.Exec(t.Context(), `UPDATE data_pipelines SET work_status=$2 WHERE id=$1`, p.ID, operationsJSON(status)); err != nil {
		t.Fatal(err)
	}
	alertDue(t, t.Context(), w, p, false)
	alertDue(t, t.Context(), w, p, true)
	history = alertHistory(t, t.Context(), store, p)
	if len(history) != 2 || history[0].Kind != "recovery" {
		t.Fatal("confirmed quality did not recover", history)
	}
}
