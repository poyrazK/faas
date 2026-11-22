package main_test

import (
	"bytes"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/routeresources"
)

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
