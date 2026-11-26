package appaccess

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// Prod hunt r28: `gregale bucket upload BUCKET …` read the bucket as the app
// and the API answered only "no such application resource". The 404 now
// names the app the request addressed; other route families keep theirs.
func TestAppNotFoundNamesTheSlug(t *testing.T) {
	mux := http.NewServeMux()
	var got string
	mux.HandleFunc("GET /v1/apps/{slug}/buckets", func(_ http.ResponseWriter, r *http.Request) {
		got = lookupProblem(r, state.ErrNotFound).Detail
	})
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/apps/bh31-bkt/buckets", nil))
	if want := `no app named "bh31-bkt" in this workspace`; got != want {
		t.Fatalf("detail = %q, want %q", got, want)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/crons/c1", nil)
	if d := lookupProblem(r, state.ErrNotFound).Detail; d != "no such cron" {
		t.Fatalf("cron detail = %q", d)
	}
}
