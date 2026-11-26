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

// --apply-split repeats the suggestion it read to the server, which applies
// it; --split-status lists splits and --undo-split reverses one
// (ADR-20261121170000 amendment 2).
func TestAppScaleApplySplit(t *testing.T) {
	split := &routeresources.Split{Kind: routeresources.SplitMemory, Route: "GET /exports/{id}", Method: "GET", PathGlob: "/exports/*",
		RouteRAMMB: 1920, CurrentRAMMB: 2048, MainRAMMB: 256, Reason: "GET /exports/{id} holds the peaks"}
	applied := api.RouteSplitResponse{ID: "split-1", Slug: "shop", TargetSlug: "shop-exports", Kind: "memory", Route: split.Route,
		Method: "GET", PathGlob: "/exports/*", RouteRAMMB: 1920, MainRAMMB: 256, PreviousRAMMB: 2048, Phase: "deploying", EdgeRuleIDs: []string{}}
	var posted api.CreateRouteSplitRequest
	var undone string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/apps/shop/analytics/route-resources":
			main.WriteJSONTest(w, api.RouteResourcesResponse{Slug: "shop", Split: split})
		case "POST /v1/apps/shop/route-splits":
			_ = json.NewDecoder(r.Body).Decode(&posted)
			w.WriteHeader(http.StatusAccepted)
			main.WriteJSONTest(w, applied)
		case "GET /v1/apps/shop/route-splits":
			done := applied
			done.Phase, done.Warning = "done", "sized down late"
			main.WriteJSONTest(w, api.RouteSplitsResponse{Splits: []api.RouteSplitResponse{done}})
		case "DELETE /v1/apps/shop/route-splits/split-1":
			undone = "split-1"
			out := applied
			out.Phase, out.MainResized = "undone", true
			main.WriteJSONTest(w, out)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")

	out, stderr, restore := main.SwapIO(t)
	if code := main.Run([]string{"app", "scale", "shop", "--suggest", "--since", "6h", "--apply-split"}); code != 0 {
		restore()
		t.Fatalf("apply exit = %d; stderr=%s", code, stderr())
	}
	restore()
	want := api.CreateRouteSplitRequest{Kind: "memory", Route: split.Route, Method: "GET", RouteRAMMB: 1920, MainRAMMB: 256, Since: "6h"}
	if posted != want {
		t.Fatalf("posted %+v, want %+v", posted, want)
	}
	if got := out.String(); !strings.Contains(got, "Split split-1 started") || !strings.Contains(got, "from 2048 MB to 256 MB") {
		t.Fatalf("apply output:\n%s", got)
	}

	out, stderr, restore = main.SwapIO(t)
	if code := main.Run([]string{"app", "scale", "shop", "--split-status"}); code != 0 {
		restore()
		t.Fatalf("status exit = %d; stderr=%s", code, stderr())
	}
	restore()
	if got := out.String(); !strings.Contains(got, "shop-exports") || !strings.Contains(got, "sized down late") {
		t.Fatalf("status output:\n%s", got)
	}

	out, stderr, restore = main.SwapIO(t)
	defer restore()
	if code := main.Run([]string{"app", "scale", "shop", "--undo-split", "split-1"}); code != 0 {
		t.Fatalf("undo exit = %d; stderr=%s", code, stderr())
	}
	if undone != "split-1" || !strings.Contains(out.String(), "at 2048 MB") {
		t.Fatalf("undo output:\n%s", out.String())
	}
}

// --apply-split with nothing to apply fails in JSON mode too, so a script
// never reads an exit 0 as an applied split.
func TestAppScaleApplySplitWithoutSuggestionFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method+" "+r.URL.Path != "GET /v1/apps/shop/analytics/route-resources" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		main.WriteJSONTest(w, api.RouteResourcesResponse{Slug: "shop"})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	for _, args := range [][]string{
		{"app", "scale", "shop", "--suggest", "--apply-split"},
		{"app", "scale", "shop", "--suggest", "--apply-split", "--json"},
	} {
		_, _, restore := main.SwapIO(t)
		code := main.Run(args)
		restore()
		if code != 1 {
			t.Errorf("%v exit = %d, want 1", args, code)
		}
	}
}
