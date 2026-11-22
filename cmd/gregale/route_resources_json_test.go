package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// route-resources (ADR-20261121170000) writes the API response as JSON under
// --json; TestJSONOutputHonored requires a test for every JSON emitter.
func TestRouteResourcesJSON(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	main.WithResetJSONOutput(t, false)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps/shop/analytics/route-resources" && r.URL.Query().Get("since") == "6h" {
			main.WriteJSONTest(w, api.RouteResourcesResponse{Slug: "shop"})
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", r.URL.String()))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	out, restoreOut := main.CaptureStdout(t)
	stderr, restoreErr := main.CaptureStderr(t)
	code := main.Run([]string{"--json", "route-resources", "shop", "--since", "6h"})
	restoreErr()
	restoreOut()
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var got api.RouteResourcesResponse
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Slug != "shop" {
		t.Fatalf("json %q: %+v %v", out.String(), got, err)
	}
}
