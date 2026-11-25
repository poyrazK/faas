package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routeresources"
)

// `--json route-resources` prints the API response as JSON, with the window
// passed through (TestJSONOutputHonored requires a JSON test per command).
func TestRouteResourcesJSON(t *testing.T) {
	var gotSince string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/shop/analytics/route-resources" {
			t.Errorf("path = %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		gotSince = r.URL.Query().Get("since")
		main.WriteJSONTest(w, api.RouteResourcesResponse{Slug: "shop", From: "2026-11-21T00:00:00Z", Until: "2026-11-22T00:00:00Z"})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	out, stderr, restore := main.SwapIO(t)
	defer restore()

	if code := main.Run([]string{"--json", "route-resources", "shop", "--since", "6h"}); code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, stderr())
	}
	var got api.RouteResourcesResponse
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil || got.Slug != "shop" {
		t.Fatalf("output = %q (%v)", out.String(), err)
	}
	if gotSince != "6h" {
		t.Errorf("since = %q, want 6h", gotSince)
	}
}

func TestSplitTargetSlug(t *testing.T) {
	for _, tc := range []struct{ slug, glob, want string }{
		{"shop", "/v1/exports/*", "shop-exports"},
		{"shop", "/pdf/*/render", "shop-render"},
		{"shop", "/*", "shop-heavy"},
		{"shop", "/Report_PDF", "shop-reportpdf"},
		{strings.Repeat("a", 60), "/export", strings.Repeat("a", 60) + "-ex"},
	} {
		if got := main.SplitTargetSlug(tc.slug, tc.glob); got != tc.want {
			t.Errorf("SplitTargetSlug(%q, %q) = %q, want %q", tc.slug, tc.glob, got, tc.want)
		}
	}
}

// The suggestion prints runnable commands in order: deploy the second app,
// size it, route the path to it, then size the main app down.
func TestRenderRouteSplitPrintsCommands(t *testing.T) {
	var out bytes.Buffer
	main.RenderRouteSplit(&out, "shop", "shop.gregale.dev", &routeresources.Split{
		Route: "POST /v1/exports/{id}", Method: "POST", PathGlob: "/v1/exports/*", RouteRAMMB: 1920, CurrentRAMMB: 2048, MainRAMMB: 256,
		Reason: "POST /v1/exports/{id} was in flight for 6 of the app's 6 memory peaks (100%)",
	})
	got := out.String()
	order := []string{
		"gregale deploy --name shop-exports",
		"gregale app scale shop-exports --plan --ram 1920",
		"gregale edge rule create --app shop --kind route --match-host shop.gregale.dev --match-path '/v1/exports/*' --match-method POST --route-target-slug shop-exports --enabled",
		"gregale app scale shop --plan --ram 256",
	}
	last := -1
	for _, want := range order {
		i := strings.Index(got, want)
		if i < 0 || i < last {
			t.Fatalf("missing or out of order %q in:\n%s", want, got)
		}
		last = i
	}
	if strings.Contains(got, "upgrade the plan") {
		t.Fatalf("over-plan note without RouteRAMOverPlan:\n%s", got)
	}
}

func TestRenderRouteSplitCPUSkipsResizing(t *testing.T) {
	var out bytes.Buffer
	main.RenderRouteSplit(&out, "shop", "shop.gregale.dev", &routeresources.Split{
		Kind: routeresources.SplitCPU, Route: "POST /v1/render", Method: "POST", PathGlob: "/v1/render",
		RouteRAMMB: 512, CurrentRAMMB: 512, MainRAMMB: 512,
		Reason: "POST /v1/render was in flight for 5 of the app's 5 CPU peaks (100%)",
	})
	got := out.String()
	for _, want := range []string{"Both apps keep 512 MB", "gregale deploy --name shop-render", "2. Send the route to it"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "app scale") {
		t.Fatalf("cpu split must not resize:\n%s", got)
	}
}
