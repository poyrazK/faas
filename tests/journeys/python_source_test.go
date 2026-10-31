package journeys

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPythonSourceDeploys deploys Python apps from source the way customers
// write them: a requirements.txt app and a uv project whose Procfile starts
// it with `uv run` (rows 2 and 3). Each must run on the shared Python runner
// base (ADR-1500 amendments 2 and 3): the interpreter serving the request is
// the base's, not Railpack's mise toolchain, which made a Python app's layer
// too big for the Free plan. Each then parks and wakes on a request.
func TestPythonSourceDeploys(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	for _, variant := range []string{"pip", "uv"} {
		t.Run(variant, func(t *testing.T) {
			t.Parallel()
			slug := tg.app(t, "py-"+variant)
			s := newStory(t)
			var base, deployID string
			if !s.step("deploy", func(t *testing.T) {
				receipt := tg.deploy(t, slug, "--path", filepath.Join("testdata", "python-"+variant))
				deployID = str(receipt, "id")
				if base = strings.TrimRight(str(receipt, "app_url"), "/"); base == "" {
					base = tg.appURL(slug)
				}
			}) {
				return
			}
			s.check("shared_runtime_interpreter", func(t *testing.T) {
				expectPythonOnSharedBase(t, base, variant, 2*time.Minute)
			})
			s.check("build_log", func(t *testing.T) {
				if deployID == "" {
					t.Skip("the deploy receipt has no deployment id")
				}
				res := tg.ok(t, time.Minute, "release", "logs", deployID, "--limit", "400")
				if !strings.Contains(string(res.stdout), "shared runtime: eligible") {
					t.Fatalf("the build log does not report the shared runtime: %s", tail(string(res.stdout), 600))
				}
			})
			s.check("park_and_wake_on_request", func(t *testing.T) {
				tg.ok(t, 2*time.Minute, "app", "park", slug)
				waitParked(t, tg, slug)
				r := get(base + "/")
				if r.status != http.StatusOK || str(jsonBody(t, r), "variant") != variant {
					t.Fatalf("first request after park: %s", r)
				}
				t.Logf("metric python_%s_wake_first_request_ms=%d", variant, r.elapsed.Milliseconds())
				expectPythonOnSharedBase(t, base, variant, time.Minute)
			})
		})
	}
}

// expectPythonOnSharedBase waits for the probe at base to answer as variant
// on Python 3.13 from the runner base: sys.base_prefix is /usr (Wolfi
// runner-python313), never Railpack's /mise toolchain.
func expectPythonOnSharedBase(t *testing.T, base, variant string, timeout time.Duration) {
	t.Helper()
	eventually(t, "GET / reports the runner base's Python", timeout, 2*time.Second, func() (bool, string) {
		r := get(base + "/")
		if r.status != http.StatusOK {
			return false, r.String()
		}
		doc := jsonBody(t, r)
		ok := str(doc, "variant") == variant && str(doc, "python") == "3.13" &&
			str(doc, "prefix") == "/app/.venv" && !strings.HasPrefix(str(doc, "base_prefix"), "/mise")
		return ok, "serving " + compact(doc)
	})
}
