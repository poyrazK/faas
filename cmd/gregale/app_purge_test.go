package main_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// `gregale app purge <slug> --yes` resolves the slug to the caller's deleted
// app in the inventory and purges that exact ID, so a Compose project torn
// down with `app delete` can deploy again under the same names.
func TestAppPurgeResolvesTheDeletedAppAndPurgesItsID(t *testing.T) {
	main.ResetJSONOut(t)
	var purged []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/deleted-apps":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"apps":[{"id":"other-id","slug":"web"},{"id":"api-id","slug":"api","restore_eligible":true}]}`)
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/deleted-apps/") && strings.HasSuffix(r.URL.Path, "/purge"):
			purged = append(purged, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/deleted-apps/"), "/purge"))
			_ = json.NewEncoder(w).Encode(api.DeletedAppPurgeResponse{ID: "api-id", Slug: "api", Purged: true})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	out, restore := main.CaptureStdout(t)
	code := main.Run([]string{"app", "purge", "api", "--yes"})
	restore()
	if code != 0 || strings.Join(purged, ",") != "api-id" || !strings.Contains(out.String(), "Purged api") {
		t.Fatalf("exit %d purged %v:\n%s", code, purged, out.String())
	}

	// A name with no deleted app purges nothing.
	purged = nil
	if code := main.Run([]string{"app", "purge", "missing", "--yes"}); code == 0 || len(purged) != 0 {
		t.Fatalf("purge of a missing app: exit %d purged %v", code, purged)
	}
}
