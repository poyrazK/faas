package dashboard

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/dataapi"
)

func TestDataAPIRenderEscapesCatalogAndShowsOwnerActions(t *testing.T) {
	attack := `<script>alert("catalog")</script>`
	data := DataAPIData{Slug: "orders", Enabled: true, CSRFToken: "sealed-token", TaskID: "task-id", TaskStatus: "succeeded", Fingerprint: strings.Repeat("a", 64), Schema: &dataapi.Snapshot{
		Schemas: []string{"api"}, Tables: []dataapi.Table{{Schema: "api", Name: attack, Columns: []dataapi.Column{{Name: "id", Type: attack}}}},
	}}
	w := httptest.NewRecorder()
	if err := Render(w, slog.New(slog.NewTextHandler(io.Discard, nil)), "test-nonce", Page{Title: "Data API", Body: "data_api", Data: data}); err != nil {
		t.Fatal(err)
	}
	body := w.Body.String()
	if strings.Contains(body, attack) || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("catalog text did not stay escaped")
	}
	for _, action := range []string{"schema", "types", "refresh"} {
		if !strings.Contains(body, "/dashboard/apps/orders/data-api/"+action) {
			t.Fatalf("missing owner action %q", action)
		}
	}
	if strings.Contains(body, `http-equiv="refresh"`) || strings.Contains(body, "Download database.types.ts") {
		t.Fatal("completed schema observation polled or offered ungenerated types")
	}
}
