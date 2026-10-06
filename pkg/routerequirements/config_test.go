package routerequirements

import (
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-436: typos, ambiguous paths, and malformed bounds must not disable checks.
func TestParseRouteRequirementsRejectsAmbiguousOrInvalidInputs(t *testing.T) {
	for name, body := range map[string]string{
		"empty":                         "",
		"version":                       `{"version":2,"routes":[]}`,
		"unknown field":                 `{"version":1,"routes":[{"method":"GET","path":"/","require":{"authentcation":"consumer"}}]}`,
		"duplicate key":                 "version: 1\nversion: 1\nroutes: []",
		"multiple documents":            "version: 1\nroutes: []\n---\nversion: 1",
		"no checks":                     `{"version":1,"routes":[{"method":"GET","path":"/","require":{}}]}`,
		"method":                        `{"version":1,"routes":[{"method":"TYPO","path":"/","require":{"authentication":"consumer"}}]}`,
		"authentication":                `{"version":1,"routes":[{"method":"GET","path":"/","require":{"authentication":"magic"}}]}`,
		"zero rate":                     `{"version":1,"routes":[{"method":"GET","path":"/","require":{"throttle":{"key_by":"none","max_rps":0}}}]}`,
		"negative rate":                 `{"version":1,"routes":[{"method":"GET","path":"/","require":{"throttle":{"key_by":"none","max_rps":-1}}}]}`,
		"nan rate":                      "version: 1\nroutes:\n  - method: GET\n    path: /\n    require:\n      throttle: {key_by: none, max_rps: .nan}",
		"inf rate":                      "version: 1\nroutes:\n  - method: GET\n    path: /\n    require:\n      throttle: {key_by: none, max_rps: .inf}",
		"unknown key dimension":         `{"version":1,"routes":[{"method":"GET","path":"/","require":{"throttle":{"key_by":"customer"}}}]}`,
		"missing key without dimension": `{"version":1,"routes":[{"method":"GET","path":"/","require":{"throttle":{"key_by":"none","missing_key_policy":"reject"}}}]}`,
		"empty budget":                  `{"version":1,"routes":[{"method":"GET","path":"/","require":{"budget":{}}}]}`,
		"zero budget":                   `{"version":1,"routes":[{"method":"GET","path":"/","require":{"budget":{"explicit":true,"max_ms":0}}}]}`,
		"duplicate route":               `{"version":1,"routes":[{"method":"GET","path":"/","require":{"authentication":"consumer"}},{"method":"get","path":"/","require":{"authentication":"consumer"}}]}`,
		"duplicate name":                `{"version":1,"routes":[{"name":"check","method":"GET","path":"/a","require":{"authentication":"consumer"}},{"name":"check","method":"GET","path":"/b","require":{"authentication":"consumer"}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(body)); err == nil {
				t.Fatal("accepted invalid requirements")
			}
		})
	}
	for _, path := range []string{"https://other.example/a", "//other/a", "/users/{id}", "/users/:id", "/users/*", "/users/[a]", "/a?token=secret", "/a#secret", "/%61", "/a/../b", "/a/./b", "/a b", "/a\\b"} {
		t.Run(path, func(t *testing.T) {
			body := fmt.Sprintf(`{"version":1,"routes":[{"method":"GET","path":%q,"require":{"authentication":"consumer"}}]}`, path)
			if _, err := Parse([]byte(body)); err == nil {
				t.Fatal("accepted a non-concrete or ambiguous path")
			}
		})
	}
}

func TestParseRouteRequirementsNormalizesMethodsAndEnforcesBounds(t *testing.T) {
	body := []byte("version: 1\nroutes:\n  - name: checkout\n    method: post\n    path: /checkout\n    require:\n      authentication: consumer\n      throttle: {key_by: consumer_id, max_rps: 10, missing_key_policy: reject}\n      budget: {explicit: true, max_ms: 2000}\n")
	config, err := Parse(body)
	if err != nil || config.Routes[0].Method != "POST" || *config.Routes[0].Require.Budget.MaxMS != 2000 {
		t.Fatalf("parse = %+v, %v", config, err)
	}
	if _, err := Parse([]byte(strings.Repeat(" ", api.RouteRequirementsMaxBytes+1))); err == nil {
		t.Fatal("accepted oversized config")
	}
	var routes strings.Builder
	routes.WriteString("version: 1\nroutes:\n")
	for i := range api.RouteRequirementsMaxRoutes + 1 {
		fmt.Fprintf(&routes, "  - method: GET\n    path: /route-%d\n    require: {authentication: consumer}\n", i)
	}
	if _, err := Parse([]byte(routes.String())); err == nil {
		t.Fatal("accepted too many checks")
	}
}
