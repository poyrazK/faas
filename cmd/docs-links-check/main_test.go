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
