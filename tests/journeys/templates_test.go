package journeys

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestTemplateDeploys deploys starter templates the way `gregale init
// --deploy` users do and calls each one: app templates over their public
// URL, function templates with `gregale invoke` (inventory rows 2, 3, 9,
// 41). Smoke covers one app and one function; full adds every GA runtime.
func TestTemplateDeploys(t *testing.T) {
	tg := needs(t, tierSmoke, true)
	t.Parallel()
	templates := []struct {
		name     string
		function bool
		tier     string
	}{
		{"hello-node", false, tierSmoke},
		{"function-node", true, tierSmoke},
		{"hello-python", false, tierFull},
		{"hello-go", false, tierFull},
		{"function-node24", true, tierFull},
		{"function-python", true, tierFull},
		{"function-python313", true, tierFull},
	}
	for _, tc := range templates {
		t.Run(tc.name, func(t *testing.T) {
			needs(t, tc.tier, true)
			t.Parallel()
			slug := tg.app(t, "t-"+tc.name)
			receipt := tg.deploy(t, slug, "--template", tc.name)
			if tc.function {
				res := tg.ok(t, 2*time.Minute, "invoke", slug, "--payload", `{"journey":"template"}`)
				out := object(t, res.stdout)
				if str(out, "status") != "completed" {
					t.Fatalf("invoke %s: %s", slug, compact(out))
				}
				return
			}
			appURL := str(receipt, "app_url")
			if appURL == "" {
				appURL = tg.appURL(slug)
			}
			r := get(strings.TrimRight(appURL, "/") + "/")
			if r.status != http.StatusOK || !strings.Contains(strings.ToLower(string(r.body)), "hello") {
				t.Fatalf("GET %s/: %s", appURL, r)
			}
		})
	}
}
