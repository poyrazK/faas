package journeys

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

// nextStaticAsset finds a /_next/static file the page links.
var nextStaticAsset = regexp.MustCompile(`/_next/static/[^"' ]+\.(?:css|js)`)

// TestJSFrameworkSourceDeploys deploys a Next.js 16 app, a Bun app, and
// pnpm and yarn apps from source (ADR-20261124160000, ADR-20261125170000).
// All must fit the Free plan's 256 MB app layer: on Free the Next.js app
// serves from its standalone tree, with its static assets and public files
// copied beside the server; the Bun app runs from mise's Bun on the shared
// Node runtime; and the pnpm and yarn apps run their start script on the
// runner base's Node, without the package manager. Each then parks and
// wakes on a request.
func TestJSFrameworkSourceDeploys(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	t.Run("nextjs", func(t *testing.T) {
		t.Parallel()
		slug := tg.app(t, "next-standalone")
		s := newStory(t)
		var base string
		if !s.step("deploy", func(t *testing.T) {
			receipt := tg.deploy(t, slug, "--path", fixtureSource(t, "nextjs-standalone"))
			if base = strings.TrimRight(str(receipt, "app_url"), "/"); base == "" {
				base = tg.appURL(slug)
			}
		}) {
			return
		}
		s.check("serves_next_server", func(t *testing.T) {
			expectNextStandalone(t, base, 2*time.Minute)
		})
		s.check("static_and_public_files", func(t *testing.T) {
			page := get(base + "/")
			if page.status != http.StatusOK || !strings.Contains(string(page.body), "journey next standalone") {
				t.Fatalf("GET /: %s", page)
			}
			asset := nextStaticAsset.FindString(string(page.body))
			if asset == "" {
				t.Fatalf("the page links no /_next/static asset: %s", tail(string(page.body), 600))
			}
			if r := get(base + asset); r.status != http.StatusOK {
				t.Fatalf("GET %s: %s", asset, r)
			}
			if r := get(base + "/hello.txt"); r.status != http.StatusOK || strings.TrimSpace(string(r.body)) != "journey public file" {
				t.Fatalf("GET /hello.txt: %s", r)
			}
		})
		s.check("park_and_wake_on_request", func(t *testing.T) {
			tg.ok(t, 2*time.Minute, "app", "park", slug)
			waitParked(t, tg, slug)
			if r := get(base + "/"); r.status != http.StatusOK {
				t.Fatalf("first request after park: %s", r)
			}
			expectNextStandalone(t, base, time.Minute)
		})
	})
	for _, pm := range []string{"pnpm", "yarn"} {
		t.Run(pm, func(t *testing.T) {
			t.Parallel()
			fixture := pm + "-hello"
			slug := tg.app(t, fixture)
			s := newStory(t)
			var base string
			if !s.step("deploy", func(t *testing.T) {
				receipt := tg.deploy(t, slug, "--path", fixtureSource(t, fixture))
				if base = strings.TrimRight(str(receipt, "app_url"), "/"); base == "" {
					base = tg.appURL(slug)
				}
			}) {
				return
			}
			s.check("runs_on_runner_node", func(t *testing.T) {
				expectRunnerNode(t, base, 2*time.Minute)
			})
			s.check("park_and_wake_on_request", func(t *testing.T) {
				tg.ok(t, 2*time.Minute, "app", "park", slug)
				waitParked(t, tg, slug)
				if r := get(base + "/"); r.status != http.StatusOK {
					t.Fatalf("first request after park: %s", r)
				}
				expectRunnerNode(t, base, time.Minute)
			})
		})
	}
	t.Run("bun", func(t *testing.T) {
		t.Parallel()
		slug := tg.app(t, "bun-hello")
		s := newStory(t)
		var base string
		if !s.step("deploy", func(t *testing.T) {
			receipt := tg.deploy(t, slug, "--path", fixtureSource(t, "bun-hello"))
			if base = strings.TrimRight(str(receipt, "app_url"), "/"); base == "" {
				base = tg.appURL(slug)
			}
		}) {
			return
		}
		s.check("runs_on_mise_bun", func(t *testing.T) {
			expectMiseBun(t, base, 2*time.Minute)
		})
		s.check("park_and_wake_on_request", func(t *testing.T) {
			tg.ok(t, 2*time.Minute, "app", "park", slug)
			waitParked(t, tg, slug)
			if r := get(base + "/"); r.status != http.StatusOK {
				t.Fatalf("first request after park: %s", r)
			}
			expectMiseBun(t, base, time.Minute)
		})
	})
}

// expectNextStandalone waits for the app at base to serve from either
// layout the build chooses: /app/.next/standalone when the full /app is over
// the account's plan's app layer cap (Free), else all of /app. It logs which.
func expectNextStandalone(t *testing.T, base string, timeout time.Duration) {
	t.Helper()
	cwd := ""
	eventually(t, "GET /api/runtime reports the Next.js server", timeout, 2*time.Second, func() (bool, string) {
		r := get(base + "/api/runtime")
		if r.status != http.StatusOK {
			return false, r.String()
		}
		doc := jsonBody(t, r)
		cwd = str(doc, "cwd")
		return cwd == "/app/.next/standalone" || cwd == "/app", "serving " + compact(doc)
	})
	t.Logf("metric nextjs_standalone=%t", cwd == "/app/.next/standalone")
}

// expectRunnerNode waits for a pnpm or yarn app at base to report that its
// start script runs on the runner base's Node, without the package manager
// (which would set npm_config_user_agent), and that its dependency loads.
func expectRunnerNode(t *testing.T, base string, timeout time.Duration) {
	t.Helper()
	eventually(t, "GET / reports the runner base's Node", timeout, 2*time.Second, func() (bool, string) {
		r := get(base + "/")
		if r.status != http.StatusOK {
			return false, r.String()
		}
		doc := jsonBody(t, r)
		return str(doc, "exec") == "/usr/local/bin/node" && str(doc, "packageManager") == "" && doc["dep"] == float64(172800000),
			"serving " + compact(doc)
	})
}

// expectMiseBun waits for the Bun app at base to report that it runs from
// mise's Bun install, the one Bun file set the app layer keeps.
func expectMiseBun(t *testing.T, base string, timeout time.Duration) {
	t.Helper()
	eventually(t, "GET / reports mise's Bun", timeout, 2*time.Second, func() (bool, string) {
		r := get(base + "/")
		if r.status != http.StatusOK {
			return false, r.String()
		}
		doc := jsonBody(t, r)
		return str(doc, "bun") != "" && strings.HasPrefix(str(doc, "exec"), "/mise/installs/bun/"), "serving " + compact(doc)
	})
}
