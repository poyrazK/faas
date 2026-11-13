package dashboard

import (
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPipelineOperabilityShowsFrozenApprovalAndHonestFreshness(t *testing.T) {
	n := int64(8192)
	data := DataPipelinesData{Enabled: true, ReadersEnabled: true, FreshnessMin: 15, FreshnessMax: 86400, IndexBudgetMax: 32 << 20, Pipelines: []api.DataPipeline{{ID: "pipeline", Name: "orders", Revision: 3, State: "live", Tables: []string{"orders"}, Freshness: &api.DataPipelineFreshness{State: "unconfirmed", Message: "An idle source may already be caught up."}, IndexAdvice: &api.DataPipelineIndexAdvice{ID: `advice" onfocus="alert(1)`, State: "preview", CanApply: true, Statement: `CREATE INDEX "<script>"`, EstimatedIndexBytes: &n, StorageBudgetBytes: 32 << 20, Message: "Writes may briefly wait."}}}}
	out := httptest.NewRecorder()
	if err := Render(out, slog.Default(), "nonce", Page{Body: "data_pipelines", Data: data}); err != nil {
		t.Fatal(err)
	}
	html := out.Body.String()
	for _, want := range []string{"An idle source may already be caught up.", `name="advice_id"`, `name="target_seconds"`, `name="index_action" value="approve"`, "Apply this index", "&lt;script&gt;"} {
		if !strings.Contains(html, want) {
			t.Error("missing control or context", want)
		}
	}
	if strings.Contains(html, `value="advice" onfocus=`) || strings.Contains(html, `<script>"`) {
		t.Fatal("unescaped advice content")
	}
}
