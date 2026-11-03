package projectaccess

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var registeredRoute = regexp.MustCompile(`mux\.Handle(?:Func)?\("([A-Z]+ /v1/[^"]+)"`)

// deliberatelyUnreviewed stays outside shared project administration (the
// routeActions comment): GitOps, full clones and browser approvals. A route
// added under one of them stays refused until someone reviews it.
var deliberatelyUnreviewed = regexp.MustCompile(`/gitops(/|$)|/environment-clones(/|$)|/approval-requests(/|$)`)

// An unreviewed project route is refused to every member of a company
// project, owners included (bug hunt 37). Every project route apidserver
// registers must have an action or be deliberately unreviewed.
func TestEveryProjectRouteIsReviewed(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "apidserver", "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("apidserver sources: %v (%d files)", err, len(files))
	}
	var missing []string
	seen := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range registeredRoute.FindAllStringSubmatch(string(src), -1) {
			pattern := m[1]
			_, path, _ := strings.Cut(pattern, " ")
			orgPath := strings.HasPrefix(path, "/v1/orgs/{organization}/") && strings.Contains(path, "/projects")
			// Wrap resolves a project from {slug} or an organization path;
			// other /v1/projects routes name none.
			if !orgPath && !(strings.HasPrefix(path, "/v1/projects/{slug}") || path == "/v1/projects") {
				continue
			}
			seen++
			if _, ok := Action(pattern); ok || deliberatelyUnreviewed.MatchString(path) {
				continue
			}
			missing = append(missing, pattern)
		}
	}
	if seen < 80 {
		t.Fatalf("found only %d project routes; the route scan no longer matches apidserver's registrations", seen)
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("%d project routes have no organization permission; add each to routeActions:\n%s", len(missing), strings.Join(missing, "\n"))
	}
}
