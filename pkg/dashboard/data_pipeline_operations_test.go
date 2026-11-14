package dashboard

import (
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPipelineScheduledControlsRenderEscapedWithRevisionAndCSRF(t *testing.T) {
	data := DataPipelinesData{Enabled: true, CSRFToken: "bound-token", Pipelines: []api.DataPipeline{{ID: "snapshot", Name: "orders", Revision: 9, DesiredState: "running", Operations: &api.DataPipelineOperations{Version: 1, Snapshot: &api.DataPipelineSnapshotSchedule{EverySeconds: 3600}}, OperationsStatus: &api.DataPipelineOperationsStatus{Snapshot: &api.DataPipelineSnapshotStatus{State: "failed", CapturedAt: "2026-11-14T10:00:00Z"}, Quality: &api.DataPipelineQualityStatus{State: "inconclusive", Results: []api.DataPipelineQualityResult{{Name: `<script>bad()</script>`, State: "inconclusive", CountIsLowerBound: true}}}}}}}
	out := httptest.NewRecorder()
	if err := Render(out, slog.Default(), "nonce", Page{Body: "data_pipelines", Data: data}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Last verified capture", "Request snapshot", "bound-token", `name="revision" value="9"`, "at least", "Set work budgets"} {
		if !strings.Contains(out.Body.String(), want) {
			t.Fatal("missing scheduled control", want)
		}
	}
	if strings.Contains(out.Body.String(), "<script>bad()</script>") {
		t.Fatal("quality name was not escaped")
	}
	if strings.Contains(out.Body.String(), `name="control" value="verify"`) {
		t.Fatal("snapshot offered a CDC verification action")
	}
}
