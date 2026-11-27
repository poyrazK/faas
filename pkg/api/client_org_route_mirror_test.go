package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// apidRoutes reads the method + pattern of every route apid registers.
func apidRoutes(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "apid", "apidserver", "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("glob apidserver sources: %v (%d files)", err, len(files))
	}
	re := regexp.MustCompile(`HandleFunc\("([A-Z]+ /v1/[^"]+)"`)
	var routes []string
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			routes = append(routes, m[1])
		}
	}
	return routes
}

var (
	routeParam       = regexp.MustCompile(`\{[^}]+\}`)
	quotedRouteParam = regexp.MustCompile(`\\\{[^}]+\\\}`)
)

// routeMatches reports whether method + path is served by pattern
// ("METHOD /v1/path/{param}").
func routeMatches(pattern, method, path string) bool {
	m, p, _ := strings.Cut(pattern, " ")
	if m != method {
		return false
	}
	re := "^" + quotedRouteParam.ReplaceAllString(regexp.QuoteMeta(p), `[^/]+`) + "$"
	return regexp.MustCompile(re).MatchString(path)
}

// TestOrgRouteTargetsRegisteredRoutes pins orgRoute to apid's routes: every
// account-wide path --org rewrites lands on a registered organization route,
// and every managed PostgreSQL path it leaves alone has no organization form
// (so a new organization mirror is picked up instead of silently skipped).
func TestOrgRouteTargetsRegisteredRoutes(t *testing.T) {
	routes := apidRoutes(t)
	registered := func(method, path string) bool {
		for _, r := range routes {
			if routeMatches(r, method, path) {
				return true
			}
		}
		return false
	}
	c := NewClient("https://api.example", "").WithProjectOrganization("acme")
	checked := 0
	for _, route := range routes {
		method, pattern, _ := strings.Cut(route, " ")
		if !strings.HasPrefix(pattern, "/v1/account") && !strings.HasPrefix(pattern, "/v1/billing") &&
			!strings.HasPrefix(pattern, "/v1/postgres") && !strings.HasPrefix(pattern, "/v1/usage") &&
			!strings.HasPrefix(pattern, "/v1/keys") && pattern != "/v1/apps" && pattern != "/v1/apps/{slug}/buckets" {
			continue
		}
		sample := routeParam.ReplaceAllString(pattern, "x1")
		got, err := c.orgRoute(method, sample)
		if err != nil {
			continue // refused before sending: never reaches a route
		}
		checked++
		if got != sample {
			if !registered(method, got) {
				t.Errorf("--org rewrites %s %s to %s, which apid does not register", method, pattern, got)
			}
			continue
		}
		if rest, ok := strings.CutPrefix(sample, "/v1/postgres/"); ok {
			if registered(method, "/v1/orgs/acme/postgres/"+rest) {
				t.Errorf("%s %s has an organization form but --org keeps the personal path; drop it from postgresFlatOnly", method, pattern)
			}
		}
	}
	if checked < 20 {
		t.Fatalf("checked only %d routes; the route scan is broken", checked)
	}
}
