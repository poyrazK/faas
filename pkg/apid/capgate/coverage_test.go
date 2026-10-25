package capgate

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/productcap"
)

// subMuxRoutes are patterns registered on a package's own mux that apid mounts
// under a catalogued prefix, so they never reach the root mux themselves.
var subMuxRoutes = map[string]string{
	"GET /{$}":     "pkg/apid/datarepair serves its review assets under /v1/apps/{slug}/data-repair",
	"GET /app.css": "pkg/apid/datarepair serves its review assets under /v1/apps/{slug}/data-repair",
}

var (
	handleCall    = regexp.MustCompile(`\.Handle(?:Func)?\(\s*"([^"]+)"(?:\s*\+\s*([A-Za-z_][A-Za-z0-9_.]*))?`)
	routeTable    = regexp.MustCompile(`^"((?:GET|POST|PUT|PATCH|DELETE|HEAD) /v1/[^"]+)":`)
	pathConstDecl = regexp.MustCompile(`^\s*(?:const\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*"(/[^"]*)"`)
)

// pathConstants maps identifiers declared as string paths in pkg/apid and
// pkg/api, so `"GET "+cliAuthPath` registrations resolve to their path.
func pathConstants(t *testing.T, root string) map[string]string {
	out := map[string]string{}
	for _, dir := range []string{filepath.Join(root, "pkg", "apid"), filepath.Join(root, "pkg", "api")} {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, line := range strings.Split(string(body), "\n") {
				if m := pathConstDecl.FindStringSubmatch(line); m != nil {
					out[m[1]] = m[2]
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// TestEveryApidRouteHasACapability fails when a route is registered on apid
// without a product capability to release it (ADR-20261023115741). Assign the
// new route's path to a capability's routes in pkg/productcap/catalog.json.
func TestEveryApidRouteHasACapability(t *testing.T) {
	catalog, err := productcap.Load()
	if err != nil {
		t.Fatal(err)
	}
	g := New(catalog, nil)
	root := filepath.Join("..", "..", "..")
	constants := pathConstants(t, root)
	var unowned []string
	for _, dir := range []string{filepath.Join(root, "pkg", "apid"), filepath.Join(root, "cmd", "apid")} {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.Contains(path, string(filepath.Separator)+"testdata"+string(filepath.Separator)) {
				return err
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, line := range strings.Split(string(body), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "//") {
					continue
				}
				patterns := []string{}
				for _, m := range handleCall.FindAllStringSubmatch(line, -1) {
					pattern := m[1]
					if m[2] != "" {
						ident := m[2][strings.LastIndex(m[2], ".")+1:]
						resolved, ok := constants[ident]
						if !ok {
							t.Errorf("cannot resolve route constant %s in %s", m[2], filepath.ToSlash(path))
							continue
						}
						pattern += resolved
					}
					patterns = append(patterns, pattern)
				}
				if m := routeTable.FindStringSubmatch(line); m != nil {
					patterns = append(patterns, m[1])
				}
				for _, pattern := range patterns {
					if _, sub := subMuxRoutes[pattern]; sub {
						continue
					}
					if g.owner(pattern) == "" {
						unowned = append(unowned, pattern+"  ("+filepath.ToSlash(path)+")")
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(unowned)
	if len(unowned) > 0 {
		t.Fatalf("%d apid routes have no capability; add each path to a capability's routes in pkg/productcap/catalog.json:\n%s", len(unowned), strings.Join(unowned, "\n"))
	}
}
