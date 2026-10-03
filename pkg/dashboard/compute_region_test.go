// ADR-465: app reads display the stored immutable compute-region choice.
package dashboard_test

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/dashboard"
)

func TestRender_StoredComputeRegion(t *testing.T) {
	for _, bodyName := range []string{"apps_list", "app_detail"} {
		for _, region := range []string{"", "eu-test", "<script>unsafe</script>"} {
			t.Run(bodyName+"/"+region, func(t *testing.T) {
				app := dashboard.AppListItem{Slug: "demo", AppID: "demo-uuid", ComputeRegion: region}
				var data any = []dashboard.AppListItem{app}
				if bodyName == "app_detail" {
					data = dashboard.AppDetailData{App: app}
				}
				rec := httptest.NewRecorder()
				page := dashboard.Page{Title: "demo", Body: bodyName, Data: data}
				if err := dashboard.Render(rec, slog.New(slog.NewTextHandler(io.Discard, nil)), "nonce", page); err != nil {
					t.Fatal(err)
				}
				body := rec.Body.String()
				want := region
				if region == "" {
					want = "Automatic placement"
				}
				if strings.HasPrefix(region, "<script>") {
					want = "&lt;script&gt;unsafe&lt;/script&gt;"
				}
				if !strings.Contains(body, want) || strings.Contains(body, "<script>unsafe</script>") {
					t.Fatalf("stored region missing or unescaped: %s", body)
				}
				if strings.Contains(body, `name="compute_region"`) {
					t.Fatal("immutable region exposed as an editable setting")
				}
			})
		}
	}
}
