// Command docs-links-check validates the source-side customer documentation
// contract. It extracts Gregale documentation URLs from code/specs and checks
// each route against docs/customer-pages.json. Dynamic error and CLI routes
// are matched by their longest registered prefix, while explicitly marked
// operator/security assets are recorded as external routes.
//
// A route may be marked unpublished, with the reason, when the docs site
// deliberately does not serve it: the page documents only preview or internal
// capabilities (#5709). The page of a GA or beta capability may not be.
//
// Go links built from the docsBase constant and a path are static, so they must name a
// registered route exactly: the live site serves only registered routes, and
// production-us answered 404 for /docs/errors/app-startup-timeout and 40 more
// such links that the prefix rule accepted (Hunt 14 H14-90).
package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/productcap"
)

const catalogPath = "docs/customer-pages.json"

var docsURLRE = regexp.MustCompile(`https://gregale\.dev/docs(/[A-Za-z0-9._~/%-]*)?`)

// docsBaseRE finds Go links built from pkg/api's docsBase constant.
var docsBaseRE = regexp.MustCompile(`docsBase\s*\+\s*"(/[A-Za-z0-9._~/%-]*)`)

// Keep the obsolete origin split in source so the checker does not match its
// own guard. pkg/wire retains the bare host solely to normalize responses from
// older servers; no production surface may emit this URL anymore.
const legacyDocsURL = "https://docs." + "gregale.dev"

type catalog struct {
	Version int     `json:"version"`
	Routes  []route `json:"routes"`
}

type route struct {
	Path     string `json:"path"`
	Source   string `json:"source,omitempty"`
	External bool   `json:"external,omitempty"`
	// Unpublished is why the docs site does not serve this page.
	Unpublished string `json:"unpublished,omitempty"`
}

type link struct {
	Path string
	File string
	// Exact links must name a registered route; the prefix rule does not
	// apply to them.
	Exact bool
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "docs-links-check:", err)
		os.Exit(1)
	}
	fmt.Println("docs-links-check: OK")
}

func run() error {
	cat, err := loadCatalog()
	if err != nil {
		return err
	}
	if err := validateCatalog(cat); err != nil {
		return err
	}
	capabilities, err := productcap.Load()
	if err != nil {
		return err
	}
	if err := checkUnpublished(cat, capabilities.Capabilities); err != nil {
		return err
	}
	links, err := collectLinks()
	if err != nil {
		return err
	}
	if err := checkExactLinks(cat, links); err != nil {
		return err
	}
	seen := map[string]link{}
	for _, l := range links {
		if _, ok := seen[l.Path]; !ok {
			seen[l.Path] = l
		}
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		r, ok := matchRoute(cat.Routes, path)
		if !ok {
			l := seen[path]
			return fmt.Errorf("%s references undocumented route %q", l.File, path)
		}
		if r.External {
			continue
		}
		if _, err := os.Stat(r.Source); err != nil {
			return fmt.Errorf("route %q source %q: %w", path, r.Source, err)
		}
	}
	return nil
}

// checkExactLinks requires every exact link to name a registered route.
func checkExactLinks(cat catalog, links []link) error {
	routes := make(map[string]bool, len(cat.Routes))
	for _, r := range cat.Routes {
		routes[r.Path] = true
	}
	for _, l := range links {
		if l.Exact && !routes[l.Path] {
			return fmt.Errorf("%s links docsBase + %q, which is no registered route; link an existing page (an #anchor may follow)", l.File, "/"+l.Path)
		}
	}
	return nil
}

func loadCatalog() (catalog, error) {
	b, err := os.ReadFile(catalogPath)
	if err != nil {
		return catalog{}, fmt.Errorf("read %s: %w", catalogPath, err)
	}
	var cat catalog
	if err := json.Unmarshal(b, &cat); err != nil {
		return catalog{}, fmt.Errorf("decode %s: %w", catalogPath, err)
	}
	return cat, nil
}

func validateCatalog(cat catalog) error {
	if cat.Version < 1 {
		return fmt.Errorf("%s version must be positive", catalogPath)
	}
	seen := map[string]bool{}
	for i, r := range cat.Routes {
		if r.Path == "" && i != 0 {
			return fmt.Errorf("%s routes[%d] root route must be first", catalogPath, i)
		}
		if seen[r.Path] {
			return fmt.Errorf("%s has duplicate route %q", catalogPath, r.Path)
		}
		seen[r.Path] = true
		if r.External == (r.Source != "") {
			return fmt.Errorf("%s route %q must have either source or external=true", catalogPath, r.Path)
		}
		if r.External && r.Unpublished != "" {
			return fmt.Errorf("%s route %q is external and cannot be unpublished", catalogPath, r.Path)
		}
	}
	return nil
}

// checkUnpublished refuses an unpublished route that is the docs page of a GA
// or beta capability: customers can use those, so their page must be served.
func checkUnpublished(cat catalog, capabilities []productcap.Capability) error {
	for _, capability := range capabilities {
		if capability.Maturity != productcap.MaturityGA && capability.Maturity != productcap.MaturityBeta {
			continue
		}
		path, ok := strings.CutPrefix(capability.DocsURL, "/docs/")
		if !ok {
			continue
		}
		path, _, _ = strings.Cut(path, "#")
		if r, ok := matchRoute(cat.Routes, strings.TrimSuffix(path, "/")); ok && r.Unpublished != "" {
			return fmt.Errorf("%s route %q is unpublished (%s) but documents %s capability %s; publish the page",
				catalogPath, r.Path, r.Unpublished, capability.Maturity, capability.ID)
		}
	}
	return nil
}

func matchRoute(routes []route, path string) (route, bool) {
	var best route
	matched := false
	for _, r := range routes {
		if path != r.Path && (r.Path == "" || !strings.HasPrefix(path, r.Path+"/")) {
			continue
		}
		if !matched || len(r.Path) > len(best.Path) {
			best, matched = r, true
		}
	}
	return best, matched
}

func collectLinks() ([]link, error) {
	var links []link
	for _, root := range []string{"api", "cmd", "pkg", "docs", "deploy", "sdk"} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if path == "docs/ops/evidence" || strings.HasPrefix(path, "docs/ops/evidence/") {
					return filepath.SkipDir
				}
				return nil
			}
			ext := filepath.Ext(path)
			switch ext {
			case ".go", ".yaml", ".yml", ".md", ".json", ".mjs", ".js", ".ts", ".py", ".html", ".service", ".toml", ".txt":
			default:
				return nil
			}
			b, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			if isProductionSurface(path) && strings.Contains(string(b), legacyDocsURL) {
				return fmt.Errorf("%s emits obsolete documentation origin %q", path, legacyDocsURL)
			}
			for _, match := range docsURLRE.FindAllStringSubmatch(string(b), -1) {
				route := strings.Trim(match[1], "/")
				route = strings.TrimRight(route, ".,;:!?)]}'\"")
				links = append(links, link{Path: route, File: path})
			}
			if ext == ".go" && isProductionSurface(path) {
				for _, match := range docsBaseRE.FindAllStringSubmatch(string(b), -1) {
					links = append(links, link{Path: strings.Trim(match[1], "/"), File: path, Exact: true})
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return links, nil
}

func isProductionSurface(path string) bool {
	if path == "pkg/wire/docs.go" || strings.HasSuffix(path, "_test.go") {
		return false
	}
	if strings.HasPrefix(path, "docs/") || strings.Contains(path, "/testdata/") {
		return false
	}
	return true
}
