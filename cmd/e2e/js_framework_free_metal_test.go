//go:build metal

// js_framework_free_metal_test.go — ADR-20261124160000 acceptance: Next.js and
// Bun source apps fit the Free plan's 256 MB app layer on the shared Node 24
// runner base, built for real in builder microVMs.
//
//   - Next.js 16 on Free: the build adapter turns on output "standalone"; the
//     full /app (about 340 MB of node_modules) is over the cap, so the build
//     keeps the standalone tree and imaged ships only it. The server runs from
//     /app/.next/standalone and serves its static assets and public files.
//   - The same app on Pro (1 GB): the full /app fits, so the build drops the
//     tree and the app ships whole, as before the ADR. (On Hobby it does not:
//     its full layer needs 545 MB of the 512 MB cap, so Hobby gets the tree.)
//   - Next.js on Free started with `next start -p $PORT -H 0.0.0.0`
//     (ADR-20261124160000 amendment 2): the flags become the standalone
//     server's environment and the app still ships only the tree.
//   - Bun on Free: imaged keeps mise's Bun install and drops mise's Node.
//   - pnpm and yarn on Free (ADR-20261125170000): the start script runs on the
//     runner base's Node, and the layer keeps only /app: no mise Node and no
//     package manager. pnpm's node_modules links resolve inside /app.
//
// Each serves when deployed and again after a park and a snapshot restore.
// The fixtures are the journey suite's (tests/journeys/testdata).
package e2e_test

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var jsNextStaticAsset = regexp.MustCompile(`/_next/static/[^"' ]+\.(?:css|js)`)

// journeyFixture tars a fixture directory of the journey suite.
func journeyFixture(t *testing.T, name string) []byte {
	t.Helper()
	return journeyFixtureWith(t, name, nil)
}

// journeyFixtureWith is journeyFixture with files replaced by replace.
func journeyFixtureWith(t *testing.T, name string, replace map[string]string) []byte {
	t.Helper()
	root := filepath.Join("..", "..", "tests", "journeys", "testdata", name)
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = string(body)
		return nil
	})
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	for path, body := range replace {
		files[path] = body
	}
	files["faas-build-token"] = name + time.Now().UTC().Format(time.RFC3339Nano) + "\n"
	return buildTarGz(t, files)
}

// get fetches path from the app through the gateway, retrying the 503s a
// loaded host answers, and returns the body and the wake id.
func (a *sharedRuntimeApp) get(t *testing.T, path string) ([]byte, string) {
	t.Helper()
	var body []byte
	var wakeID string
	status := 0
	for attempt := 0; attempt < 4; attempt++ {
		body, wakeID, status = doGetWithHostCapturingWakeID(t, a.h.HTTPClient(), a.h.GatewayURL+path, a.slug+".apps.test.example", 90*time.Second)
		if status != http.StatusServiceUnavailable {
			break
		}
		t.Logf("GET %s attempt %d: 503 %s", path, attempt+1, body)
		time.Sleep(5 * time.Second)
	}
	if status != http.StatusOK {
		t.Fatalf("GET %s: status=%d body=%q", path, status, tailString(string(body), 600))
	}
	return body, wakeID
}

func (a *sharedRuntimeApp) getJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	body, _ := a.get(t, path)
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("GET %s: not JSON: %q", path, tailString(string(body), 600))
	}
	return doc
}

func tailString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func TestJSFrameworksFitFreeMetal(t *testing.T) {
	seed := newSharedRuntimeApp(t, "js-seed", "unused")
	if seed == nil {
		return
	}
	h, pool := seed.h, seed.pool
	// newSharedRuntimeApp seeds Hobby; each app below gets its own account.
	nextFree := addSharedRuntimeApp(t, h, pool, api.PlanFree, "next-free")
	nextPro := addSharedRuntimeApp(t, h, pool, api.PlanPro, "next-pro")
	nextFlags := addSharedRuntimeApp(t, h, pool, api.PlanFree, "next-flags")
	bunFree := addSharedRuntimeApp(t, h, pool, api.PlanFree, "bun-free")
	pnpmFree := addSharedRuntimeApp(t, h, pool, api.PlanFree, "pnpm-free")
	yarnFree := addSharedRuntimeApp(t, h, pool, api.PlanFree, "yarn-free")

	guestInit, err := os.Stat(os.Getenv("FAAS_GUEST_INIT"))
	if err != nil {
		t.Fatalf("stat FAAS_GUEST_INIT: %v", err)
	}
	freeCap := int64(api.MustLimitsFor(api.PlanFree).AppLayerMaxMB) << 20

	t.Run("nextjs-free-standalone", func(t *testing.T) {
		dep := nextFree.deploy(t, journeyFixture(t, "nextjs-standalone"), "cccccccc-0000-4000-8000-000000000001", sharedBaseStallWindow, sharedBaseCeiling)
		t.Logf("next free: runtime_base=%q app layer=%d bytes (guest-init %d)", dep.RuntimeBase, dep.RootfsBytes, guestInit.Size())
		if dep.RuntimeBase != "node24" {
			t.Fatalf("runtime_base = %q, want node24", dep.RuntimeBase)
		}
		// The standalone tree of this app is about 42 MB.
		if limit := guestInit.Size() + 96<<20; dep.RootfsBytes <= 0 || dep.RootfsBytes > limit || dep.RootfsBytes > freeCap {
			t.Fatalf("app layer = %d bytes, want the standalone tree: at most %d", dep.RootfsBytes, limit)
		}
		check := func(t *testing.T) {
			if cwd := nextFree.getJSON(t, "/api/runtime")["cwd"]; cwd != "/app/.next/standalone" {
				t.Fatalf("server cwd = %v, want /app/.next/standalone", cwd)
			}
			page, _ := nextFree.get(t, "/")
			if !strings.Contains(string(page), "journey next standalone") {
				t.Fatalf("GET /: %q", tailString(string(page), 600))
			}
			asset := jsNextStaticAsset.FindString(string(page))
			if asset == "" {
				t.Fatalf("the page links no /_next/static asset: %q", tailString(string(page), 600))
			}
			nextFree.get(t, asset)
			if hello, _ := nextFree.get(t, "/hello.txt"); strings.TrimSpace(string(hello)) != "journey public file" {
				t.Fatalf("GET /hello.txt = %q", hello)
			}
		}
		check(t)
		nextFree.park(t)
		check(t)
		nextFree.park(t)
	})

	t.Run("nextjs-free-start-flags", func(t *testing.T) {
		pkg, err := os.ReadFile(filepath.Join("..", "..", "tests", "journeys", "testdata", "nextjs-standalone", "package.json"))
		if err != nil {
			t.Fatal(err)
		}
		flagged := strings.Replace(string(pkg), `"start": "next start"`, `"start": "next start -p $PORT -H 0.0.0.0"`, 1)
		if flagged == string(pkg) {
			t.Fatal("fixture start script is not `next start`")
		}
		dep := nextFlags.deploy(t, journeyFixtureWith(t, "nextjs-standalone", map[string]string{"package.json": flagged}), "cccccccc-0000-4000-8000-000000000004", sharedBaseStallWindow, sharedBaseCeiling)
		t.Logf("next flags: runtime_base=%q app layer=%d bytes", dep.RuntimeBase, dep.RootfsBytes)
		if limit := guestInit.Size() + 96<<20; dep.RootfsBytes <= 0 || dep.RootfsBytes > limit {
			t.Fatalf("app layer = %d bytes, want the standalone tree: at most %d", dep.RootfsBytes, limit)
		}
		check := func(t *testing.T) {
			if cwd := nextFlags.getJSON(t, "/api/runtime")["cwd"]; cwd != "/app/.next/standalone" {
				t.Fatalf("server cwd = %v, want /app/.next/standalone", cwd)
			}
		}
		check(t)
		nextFlags.park(t)
		check(t)
		nextFlags.park(t)
	})

	t.Run("nextjs-pro-full-app", func(t *testing.T) {
		dep := nextPro.deploy(t, journeyFixture(t, "nextjs-standalone"), "cccccccc-0000-4000-8000-000000000002", sharedBaseStallWindow, sharedBaseCeiling)
		t.Logf("next pro: runtime_base=%q app layer=%d bytes", dep.RuntimeBase, dep.RootfsBytes)
		if dep.RuntimeBase != "node24" {
			t.Fatalf("runtime_base = %q, want node24", dep.RuntimeBase)
		}
		// The full /app is over the Free cap; Pro's fits it.
		if dep.RootfsBytes <= freeCap {
			t.Fatalf("app layer = %d bytes, want the full /app (over %d)", dep.RootfsBytes, freeCap)
		}
		if cwd := nextPro.getJSON(t, "/api/runtime")["cwd"]; cwd != "/app" {
			t.Fatalf("server cwd = %v, want /app", cwd)
		}
		nextPro.get(t, "/hello.txt")
		nextPro.park(t)
	})

	t.Run("bun-free", func(t *testing.T) {
		dep := bunFree.deploy(t, journeyFixture(t, "bun-hello"), "cccccccc-0000-4000-8000-000000000003", sharedBaseStallWindow, sharedBaseCeiling)
		t.Logf("bun free: runtime_base=%q app layer=%d bytes", dep.RuntimeBase, dep.RootfsBytes)
		if dep.RuntimeBase != "node24" {
			t.Fatalf("runtime_base = %q, want node24", dep.RuntimeBase)
		}
		// Bun is about 90 MB; mise's Node would add about 200 MB more.
		if limit := guestInit.Size() + 140<<20; dep.RootfsBytes <= 0 || dep.RootfsBytes > limit {
			t.Fatalf("app layer = %d bytes, want Bun without Node: at most %d", dep.RootfsBytes, limit)
		}
		check := func(t *testing.T) {
			doc := bunFree.getJSON(t, "/")
			if exec, _ := doc["exec"].(string); doc["bun"] == nil || !strings.HasPrefix(exec, "/mise/installs/bun/") {
				t.Fatalf("GET / = %v, want Bun from /mise/installs/bun", doc)
			}
		}
		check(t)
		bunFree.park(t)
		check(t)
		bunFree.park(t)
	})

	for i, tc := range []struct {
		pm  string
		app *sharedRuntimeApp
	}{{"pnpm", pnpmFree}, {"yarn", yarnFree}} {
		t.Run(tc.pm+"-free", func(t *testing.T) {
			id := "cccccccc-0000-4000-8000-00000000001" + string(rune('0'+i))
			dep := tc.app.deploy(t, journeyFixture(t, tc.pm+"-hello"), id, sharedBaseStallWindow, sharedBaseCeiling)
			t.Logf("%s free: runtime_base=%q app layer=%d bytes", tc.pm, dep.RuntimeBase, dep.RootfsBytes)
			if dep.RuntimeBase != "node24" {
				t.Fatalf("runtime_base = %q, want node24", dep.RuntimeBase)
			}
			// /app is a few hundred KB; mise's Node and the package manager
			// would add about 200 MB.
			if limit := guestInit.Size() + 16<<20; dep.RootfsBytes <= 0 || dep.RootfsBytes > limit {
				t.Fatalf("app layer = %d bytes, want only /app: at most %d", dep.RootfsBytes, limit)
			}
			check := func(t *testing.T) {
				doc := tc.app.getJSON(t, "/")
				if doc["exec"] != "/usr/local/bin/node" || doc["packageManager"] != "" || doc["dep"] != float64(172800000) {
					t.Fatalf("GET / = %v, want the runner base's Node without %s and the ms dependency", doc, tc.pm)
				}
			}
			check(t)
			tc.app.park(t)
			check(t)
			tc.app.park(t)
		})
	}
}
