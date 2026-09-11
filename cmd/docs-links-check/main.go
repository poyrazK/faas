// Command docs-links-check validates the source-side customer documentation
// contract. It extracts Gregale documentation URLs from code/specs and checks
// each route against docs/customer-pages.json. Dynamic error and CLI routes
// are matched by their longest registered prefix, while explicitly marked
// operator/security assets are recorded as external routes.
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
)

const catalogPath = "docs/customer-pages.json"

var docsURLRE = regexp.MustCompile(`https://(?:gregale\.dev/docs|docs\.gregale\.dev)(/[A-Za-z0-9._~/%-]*)?`)

type catalog struct {
	Version int     `json:"version"`
	Routes  []route `json:"routes"`
}

type route struct {
	Path     string `json:"path"`
	Source   string `json:"source,omitempty"`
	External bool   `json:"external,omitempty"`
}

type link struct {
	Path string
	File string
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
	links, err := collectLinks()
	if err != nil {
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
	for _, root := range []string{"api", "cmd", "pkg", "docs"} {
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
			if ext != ".go" && ext != ".yaml" && ext != ".yml" && ext != ".md" && ext != ".json" {
				return nil
			}
			b, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			for _, match := range docsURLRE.FindAllStringSubmatch(string(b), -1) {
				route := strings.Trim(match[1], "/")
				route = strings.TrimRight(route, ".,;:!?)]}'\"")
				links = append(links, link{Path: route, File: path})
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return links, nil
}
