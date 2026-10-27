package journeys

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestTemplateDeploys deploys starter templates the way `gregale init
// --deploy` users do and calls each one: app templates over their public
// URL, function templates with `gregale invoke`, workers and crons by their
// live receipt (rows 2, 3, 9, 41). Smoke covers one app and one function;
// full covers every template that needs no backing service. Templates that
// need Postgres, object storage, AI provider keys, MCP hosting or other
// preview products are left to those products' journeys.
func TestTemplateDeploys(t *testing.T) {
	tg := needs(t, tierSmoke, true)
	t.Parallel()
	const (
		web      = "web"
		function = "function"
		workload = "workload" // no public HTTP: a live receipt is the check
	)
	templates := []struct {
		name, kind, tier string
	}{
		{"hello-node", web, tierSmoke},
		{"function-node", function, tierSmoke},
		{"hello-python", web, tierFull},
		{"hello-go", web, tierFull},
		{"function-node24", function, tierFull},
		{"function-python", function, tierFull},
		{"function-python313", function, tierFull},
		{"function-go", function, tierFull},
		{"function-rust", function, tierFull},
		{"cron-example", workload, tierFull},
		{"cron-worker", workload, tierFull},
		{"event-worker", workload, tierFull},
		{"queue-worker", workload, tierFull},
		{"webhook-receiver", workload, tierFull},
	}
	for _, tc := range templates {
		t.Run(tc.name, func(t *testing.T) {
			needs(t, tc.tier, true)
			t.Parallel()
			slug := tg.app(t, "t-"+tc.name)
			receipt := tg.deployWith(t, slug, tc.kind != workload, "--template", tc.name)
			switch tc.kind {
			case function:
				res := tg.ok(t, 2*time.Minute, "invoke", slug, "--payload", `{"journey":"template"}`)
				if out := object(t, res.stdout); str(out, "status") != "completed" {
					t.Fatalf("invoke %s: %s", slug, compact(out))
				}
			case web:
				appURL := str(receipt, "app_url")
				if appURL == "" {
					appURL = tg.appURL(slug)
				}
				r := get(strings.TrimRight(appURL, "/") + "/")
				if r.status != http.StatusOK || !strings.Contains(strings.ToLower(string(r.body)), "hello") {
					t.Fatalf("GET %s/: %s", appURL, r)
				}
			case workload:
				t.Logf("%s is live: rollout %s, smoke %s", slug, str(receipt, "rollout_state"),
					str(receipt, "hosting_receipt.smoke.status"))
			}
		})
	}
}
