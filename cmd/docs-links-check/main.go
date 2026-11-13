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
// A published page may not carry operator or engineering material
// (ADR-20261028200304): decision-record references, installation flags,
// daemon names, repository paths or spec sections. That material lives in
// docs/ops/<page>-operator-notes.md. Every GA or beta capability's docs page
// must also be a registered route.
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
	if err := checkCapabilityRoutes(cat, capabilities.Capabilities); err != nil {
		return err
	}
	if err := checkPublishedContent(cat, os.ReadFile); err != nil {
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

// checkCapabilityRoutes requires the docs page of every GA or beta capability
// to be a registered route: the CLI and API errors link customers there.
func checkCapabilityRoutes(cat catalog, capabilities []productcap.Capability) error {
	for _, capability := range capabilities {
		if capability.Maturity != productcap.MaturityGA && capability.Maturity != productcap.MaturityBeta {
			continue
		}
		path, ok := strings.CutPrefix(strings.TrimPrefix(capability.DocsURL, "https://gregale.dev"), "/docs")
		if !ok {
			continue
		}
		path, _, _ = strings.Cut(path, "#")
		path = strings.Trim(path, "/")
		if _, ok := matchRoute(cat.Routes, path); !ok {
			return fmt.Errorf("%s capability %s documents %s, which is no route in %s; add the route",
				capability.Maturity, capability.ID, capability.DocsURL, catalogPath)
		}
	}
	return nil
}

// internalMarkerRE finds operator and engineering material on a customer
// page. Each alternative names something a customer cannot act on.
var internalMarkerRE = regexp.MustCompile(strings.Join([]string{
	`\bADRs?[- ]?\d`,
	`\]\((?:\.\./)*(?:adr|ops|runbooks|drills)/`,
	`\b(?:apid|schedd|vmmd|builderd|imaged|meterd|githubd|outboundd|pgproxyd|gatewayd(?:-public|-internal)?)\b`,
	`\bpkg/(?:api|apid|state|sched|fcvm|netns|oci|rootfs|meter|billing|wire|imaged|builderd|githubd|legal|secretbox|servicecaller)\b`,
	`\bmigrations/\d`,
	`faas_implementation_spec|\bspec ?§|spec-§`,
	`\bFAAS_[A-Z0-9_]+`,
	`/etc/faas/(?:secrets|[a-z-]+\.env)|journalctl|systemctl`,
	`native (?:qualification|acceptance|KVM)|qualified (?:server|installation)|qualification harness`,
	`operators? (?:can|must|may) (?:set|enable|configure)|ask (?:your|the) operator|Gregale operator`,
}, "|"))

// customerEnvRE matches the FAAS_ names customers do use: the CLI's settings
// and the variables Gregale sets inside an app.
var customerEnvRE = regexp.MustCompile(`^FAAS_(?:TOKEN|API|JSON|COMPLETION_CACHE_PATH|DEVELOPER_ID|DEVELOPER_ENVIRONMENT|RUNTIME|APP_ID|APP_SLUG|TENANT_ID|DEPLOYMENT_ID|DEPLOYMENT_TAG|DEPLOYMENT_CREATED_AT|INSTANCE_ID|INSTANCE_IDENTITY_FILE|NODE_ID|REGION|COMMIT_SHA|IMAGE_DIGEST|EVENT_PUBLISH_ENDPOINT|METADATA_ENV_ENDPOINT|SECRETS_[A-Z_]+|WORKLOAD_[A-Z0-9_]*)$`)

// checkPublishedContent refuses internal material on every page the docs
// site serves.
func checkPublishedContent(cat catalog, read func(string) ([]byte, error)) error {
	var findings []string
	seen := map[string]bool{}
	for _, r := range cat.Routes {
		if r.Source == "" || r.Unpublished != "" || seen[r.Source] {
			continue
		}
		seen[r.Source] = true
		b, err := read(r.Source)
		if err != nil {
			return fmt.Errorf("route %q source %q: %w", r.Path, r.Source, err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			for _, m := range internalMarkerRE.FindAllString(line, -1) {
				if customerEnvRE.MatchString(m) {
					continue
				}
				findings = append(findings, fmt.Sprintf("%s:%d: published page %q mentions internal %q", r.Source, i+1, r.Path, m))
			}
		}
	}
	if len(findings) > 0 {
		return fmt.Errorf("move operator material to docs/ops/<page>-operator-notes.md (ADR-20261028200304):\n  %s",
			strings.Join(findings, "\n  "))
	}
	return nil
}
