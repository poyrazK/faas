package appaccess

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/authz"
)

// appAddressedPrefixes are the paths Wrap resolves an app from (addressedApp
// and indirectlyAddressedApp).
var appAddressedPrefixes = []string{
	"/v1/apps/{slug}", "/v1/builds/{id}", "/v1/deployments/{id}", "/v1/crons/{id}",
	"/v1/domains/{domain}", "/v1/edge-rules/{id}", "/v1/invocations/{id}",
}

var registeredRoute = regexp.MustCompile(`mux\.Handle(?:Func)?\("([A-Z]+ /v1/[^"]+)"`)

// An app endpoint missing from the reviewed maps falls back to the creator
// alone, so a company app's other members cannot use it (bug hunt 37). Every
// endpoint apidserver registers under an app-addressed path must be reviewed:
// in routeActions (featureRouteActions included) or, for object storage and
// queues, in authz's own maps.
func TestEveryAppRouteIsReviewed(t *testing.T) {
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
			if !appAddressed(path) {
				continue
			}
			seen++
			if reviewed(pattern) {
				continue
			}
			missing = append(missing, pattern)
		}
	}
	if seen < 500 {
		t.Fatalf("found only %d app endpoints; the route scan no longer matches apidserver's registrations", seen)
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("%d app endpoints have no organization permission; add each to routeActions or featureRouteActions:\n%s",
			len(missing), strings.Join(missing, "\n"))
	}
}

func appAddressed(path string) bool {
	for _, prefix := range appAddressedPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") || strings.HasPrefix(path, prefix+":") {
			return true
		}
	}
	return false
}

func reviewed(pattern string) bool {
	if _, ok := routeActions[pattern]; ok {
		return true
	}
	if authz.QueuePattern(pattern) {
		_, ok := authz.QueueAction(pattern)
		return ok
	}
	if authz.ObjectStoragePattern(pattern) {
		_, ok := authz.ObjectStorageAction(pattern)
		return ok
	}
	return false
}
