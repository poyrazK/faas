package main

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/productcap"
)

// Hunt 14 H14-90: API problems linked /docs/errors/app-startup-timeout and 40
// more sub-paths the live site never served; the prefix rule accepted them
// and the checker did not read links built from docsBase at all.
func TestDocsBaseLinksMustNameARegisteredRoute(t *testing.T) {
	src := `WithDocs(docsBase + "/errors#app_startup_timeout")
		WithDocs(docsBase+"/errors/app-startup-timeout")`
	var paths []string
	for _, m := range docsBaseRE.FindAllStringSubmatch(src, -1) {
		paths = append(paths, strings.Trim(m[1], "/"))
	}
	if len(paths) != 2 || paths[0] != "errors" || paths[1] != "errors/app-startup-timeout" {
		t.Fatalf("docsBase links = %q", paths)
	}
	cat := catalog{Version: 1, Routes: []route{{Path: "errors", Source: "docs/errors.md"}}}
	if err := checkExactLinks(cat, []link{{Path: paths[0], File: "a.go", Exact: true}}); err != nil {
		t.Fatalf("registered route with an anchor refused: %v", err)
	}
	err := checkExactLinks(cat, []link{{Path: paths[1], File: "b.go", Exact: true}})
	if err == nil || !strings.Contains(err.Error(), "errors/app-startup-timeout") {
		t.Fatalf("unregistered sub-path accepted: %v", err)
	}
	// Literal URLs keep the longest-prefix rule.
	if err := checkExactLinks(cat, []link{{Path: paths[1], File: "c.md"}}); err != nil {
		t.Fatalf("a non-exact link is judged by the prefix rule elsewhere: %v", err)
	}
}

// #5709: the docs site withholds the pages of preview and internal
// capabilities. A route may say so, but never for a GA or beta capability's
// page, which customers can reach from the CLI and API errors.
func TestUnpublishedRoutesOnlyForPreviewAndInternalPages(t *testing.T) {
	cat := catalog{Version: 1, Routes: []route{
		{Path: "crons", Source: "docs/crons.md", Unpublished: "operator notes"},
		{Path: "mcp", Source: "docs/mcp.md", Unpublished: "preview capability"},
	}}
	preview := []productcap.Capability{{ID: "mcp-hosting", Maturity: productcap.MaturityPreview, DocsURL: "/docs/mcp"}}
	if err := checkUnpublished(cat, preview); err != nil {
		t.Fatalf("a preview capability's page refused: %v", err)
	}
	for _, maturity := range []productcap.Maturity{productcap.MaturityGA, productcap.MaturityBeta} {
		err := checkUnpublished(cat, []productcap.Capability{{ID: "crons", Maturity: maturity, DocsURL: "/docs/crons#schedules"}})
		if err == nil || !strings.Contains(err.Error(), `"crons"`) {
			t.Fatalf("%s capability's unpublished page accepted: %v", maturity, err)
		}
	}
	external := catalog{Version: 1, Routes: []route{{Path: "status", External: true, Unpublished: "x"}}}
	if err := validateCatalog(external); err == nil {
		t.Fatal("an external route marked unpublished was accepted")
	}
}

// ADR-20261028200304: published pages say what a customer sees and can do,
// never how an installation is configured.
func TestPublishedPagesRefuseInternalMaterial(t *testing.T) {
	cat := catalog{Version: 1, Routes: []route{
		{Path: "a", Source: "a.md"},
		{Path: "b", Source: "b.md", Unpublished: "preview capability"},
	}}
	page := map[string]string{"b.md": "operators can set FAAS_SECRET_FLAG (ADR-123)"}
	read := func(name string) ([]byte, error) { return []byte(page[name]), nil }
	for _, ok := range []string{
		"Set `FAAS_TOKEN` in CI. The app reads `FAAS_APP_ID` and `FAAS_WORKLOAD_<NAME>_ADDR`.",
		"A handler at `/internal/after-restore` and `go run ./cmd/api` are customer paths.",
		"The DPA §7 notice window.",
	} {
		page["a.md"] = ok
		if err := checkPublishedContent(cat, read); err != nil {
			t.Errorf("customer text %q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"See [ADR-529](adr/529-binding-gated-promotion.md).",
		"enabled with `FAAS_PROMOTION_HEALTH_ENABLED=1`",
		"the vmmd HTTP bridge",
		"`pkg/api/limits.go` holds the table",
		"spec §11 requires it",
		"pending [native qualification](ops/promotion.md)",
		"until the operator enables it, ask your operator",
		"check `journalctl -u faas-apid`",
	} {
		page["a.md"] = "intro\n" + bad
		err := checkPublishedContent(cat, read)
		if err == nil || !strings.Contains(err.Error(), "a.md:2") {
			t.Errorf("internal text %q accepted: %v", bad, err)
		}
	}
}

// A GA or beta capability's page must be a registered route: cache,
// container-compatibility and inbound-webhooks had none, so the site never
// served them.
func TestCapabilityPagesMustBeRoutes(t *testing.T) {
	cat := catalog{Version: 1, Routes: []route{{Path: "", Source: "docs/quickstart.md"}, {Path: "postgres", Source: "docs/postgres.md"}}}
	ok := []productcap.Capability{
		{ID: "pg", Maturity: productcap.MaturityGA, DocsURL: "/docs/postgres#restore"},
		{ID: "abs", Maturity: productcap.MaturityBeta, DocsURL: "https://gregale.dev/docs/postgres"},
		{ID: "preview", Maturity: productcap.MaturityPreview, DocsURL: "/docs/nowhere"},
	}
	if err := checkCapabilityRoutes(cat, ok); err != nil {
		t.Fatalf("registered pages refused: %v", err)
	}
	err := checkCapabilityRoutes(cat, []productcap.Capability{{ID: "cache", Maturity: productcap.MaturityBeta, DocsURL: "/docs/cache"}})
	if err == nil || !strings.Contains(err.Error(), "cache") {
		t.Fatalf("unregistered capability page accepted: %v", err)
	}
}
