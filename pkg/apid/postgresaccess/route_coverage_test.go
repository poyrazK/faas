package postgresaccess

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var registeredRoute = regexp.MustCompile(`mux\.Handle(?:Func)?\("([A-Z]+ /v1/[^"]+)"`)

// deliberatelyUnreviewed stays closed to shared administration
// (TestUnreviewedPostgresActionsStayClosed): database cutovers.
var deliberatelyUnreviewed = regexp.MustCompile(`/postgres/cutovers(/|$)`)

// An unreviewed database route is refused to every member of a company
// database, owners included (bug hunt 37). Every managed Postgres route
// apidserver registers must have an action.
func TestEveryPostgresRouteIsReviewed(t *testing.T) {
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
			if !strings.HasPrefix(path, "/v1/postgres/") && !strings.HasPrefix(path, "/v1/orgs/{organization}/postgres/") {
				continue
			}
			seen++
			if _, ok := Action(pattern); !ok && !deliberatelyUnreviewed.MatchString(path) {
				missing = append(missing, pattern)
			}
		}
	}
	if seen < 20 {
		t.Fatalf("found only %d database routes; the route scan no longer matches apidserver's registrations", seen)
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("%d database routes have no organization permission; add each to actions:\n%s", len(missing), strings.Join(missing, "\n"))
	}
}
