package dashboard

import (
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPipelineEvolutionReviewIsEscapedAndApprovalUsesSavedID(t *testing.T) {
	c := &api.DataPipelineEvolution{ID: `saved" onfocus="alert(1)`, State: "preview", Kind: "projection", After: &api.DataPipelineServing{Version: 2, Columns: map[string][]string{"orders": {"id", "<script>"}}}}
	data := DataPipelinesData{Enabled: true, CSRFToken: "verified-token", Evolutions: []DataPipelineEvolution{
		{Pipeline: api.DataPipeline{ID: "legacy", Name: "legacy", Revision: 1, Tables: []string{"orders"}}, CanPreview: true},
		{Pipeline: api.DataPipeline{ID: "projected", Name: "projected", Revision: 2, Evolution: c, Serving: &api.DataPipelineServing{Version: 1, Columns: map[string][]string{"orders": {"id"}}}}, CanPreview: true, CanApprove: true, CanDiscard: true},
	}}
	out := httptest.NewRecorder()
	if err := Render(out, slog.Default(), "nonce", Page{Body: "data_pipelines", Data: data}); err != nil {
		t.Fatal(err)
	}
	body := out.Body.String()
	for _, want := range []string{"Grow your customer API", "&lt;script&gt;", `name="action" value="evolution_approve"`, `name="confirm_evolution"`, `name="csrf_token" value="verified-token"`, "Preview selection"} {
		if !strings.Contains(body, want) {
			t.Error("missing reviewed control", want)
		}
	}
	if strings.Contains(body, `value="saved" onfocus=`) || strings.Contains(body, "<code><script>") {
		t.Fatal("unescaped evolution metadata")
	}
}
