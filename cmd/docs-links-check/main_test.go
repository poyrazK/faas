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

// 189 production links named sections no published page had (plans#apps,
// orgs#members): the site opened them at the top of the page.
func TestAnchorsMustNameAHeading(t *testing.T) {
	page := "# Plans\n\n## At a glance\n\n### `plan_limit_apps`\n\n```bash\n## not-a-heading\n```\n\n" +
		"## Opting out: keep N instances warm (Pro, Scale)\n\n## See [the CLI](cli.md) **now**\n"
	cat := catalog{Version: 1, Routes: []route{
		{Path: "plans", Source: "plans.md"},
		{Path: "events", Source: "events.md", Unpublished: "preview capability"},
	}}
	read := func(name string) ([]byte, error) { return []byte(page), nil }
	tests := []struct {
		name string
		l    link
		ok   bool
	}{
		{"level-2 heading", link{Path: "plans", Anchor: "at-a-glance", File: "pkg/api/errors.go"}, true},
		{"code heading", link{Path: "plans", Anchor: "plan_limit_apps", File: "pkg/api/errors.go"}, true},
		{"punctuation dropped", link{Path: "plans", Anchor: "opting-out-keep-n-instances-warm-pro-scale", File: "pkg/api/errors.go"}, true},
		{"link and emphasis text", link{Path: "plans", Anchor: "see-the-cli-now", File: "pkg/api/errors.go"}, true},
		{"no such heading", link{Path: "plans", Anchor: "apps", File: "pkg/api/errors.go"}, false},
		{"heading inside a code fence", link{Path: "plans", Anchor: "not-a-heading", File: "pkg/api/errors.go"}, false},
		{"unpublished page", link{Path: "events", Anchor: "anything", File: "pkg/api/errors.go"}, true},
		{"test file", link{Path: "plans", Anchor: "apps", File: "pkg/api/errors_test.go"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkAnchors(cat, []link{tt.l}, read)
			if tt.ok && err != nil {
				t.Fatalf("refused: %v", err)
			}
			if !tt.ok && (err == nil || !strings.Contains(err.Error(), "plans#"+tt.l.Anchor)) {
				t.Fatalf("accepted: %v", err)
			}
		})
	}
	for _, m := range [][2]string{
		{`WithDocs(docsBase + "/plans#at-a-glance")`, "at-a-glance"},
		{`"https://gregale.dev/docs/plans#apps".`, "apps"},
	} {
		re := docsBaseRE
		if strings.HasPrefix(m[0], `"https`) {
			re = docsURLRE
		}
		if got := re.FindStringSubmatch(m[0]); got == nil || got[2] != m[1] {
			t.Errorf("anchor of %s = %q, want %q", m[0], got, m[1])
		}
	}
}

// llms.txt audit 2026-11-17: `[Jobs](jobs)` and `[Penalty box](security#penalty-box)`
// on published pages named no file, so the docs site linked them to GitHub
// paths that answer 404.
func TestPublishedPagesLinkExistingFiles(t *testing.T) {
	cat := catalog{Version: 1, Routes: []route{
		{Path: "crons", Source: "docs/crons.md"},
		{Path: "hidden", Source: "docs/hidden.md", Unpublished: "preview"},
	}}
	pages := map[string]string{
		"docs/crons.md": "See [jobs](jobs.md), [apps](apps.md#tasks), [site](/docs/x), [web](https://a.b/c), [here](#top).\n" +
			"Broken: [jobs](jobs) and [box](security#penalty-box).\n" +
			"Code: `[x](nope)`\n```md\n[y](nope)\n```\n",
		"docs/hidden.md": "[gone](gone)\n",
	}
	files := map[string]bool{"docs/jobs.md": true, "docs/apps.md": true, "docs/security.md": true}
	read := func(p string) ([]byte, error) { return []byte(pages[p]), nil }
	err := checkRelativeLinks(cat, read, func(p string) bool { return files[p] })
	if err == nil {
		t.Fatal("links to missing files accepted")
	}
	msg := err.Error()
	for _, want := range []string{`docs/crons.md:2: link "jobs"`, `docs/crons.md:2: link "security#penalty-box"`} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q does not name %s", msg, want)
		}
	}
	for _, unwanted := range []string{"nope", "gone", "jobs.md", "apps.md", "/docs/x", "https://", "#top"} {
		if strings.Contains(msg, `"`+unwanted) {
			t.Fatalf("error %q flags %s", msg, unwanted)
		}
	}
	files["docs/jobs"] = true
	files["docs/security"] = true
	if err := checkRelativeLinks(cat, read, func(p string) bool { return files[p] }); err != nil {
		t.Fatalf("existing targets refused: %v", err)
	}
}
