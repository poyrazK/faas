//go:build metal

// dev_watch_metal_test.go — ADR-1170 developer watch mode on a real
// Firecracker stack.
//
//	PUT /v1/dev/sessions {watch}  → developer app in watch mode
//	dev-source sync #1            → builder VM applies the watch overlay to a
//	                                Node app WITH a build script; the plan is
//	                                verbatim and recorded as a watch build
//	                                → live, served by `npm run dev`
//	dev-source sync #2            → live patch reaches the running
//	                                development server, which reloads it
//	                                itself; the edit is served by the same
//	                                instance before the second build is live
//
// The fixture's server behaves like Vite's default: it listens on
// 127.0.0.1:5173, not on the runtime port, and answers 403 to a Host header
// it was not told about. It is reachable only because guest-init bridges the
// runtime port to it in a watch-mode image, and serves the environment's
// hostname only because the image carries it in GREGALE_DEV_HOST and Vite's
// allowed-hosts variable. The second build changes no dependency input, so
// it keeps the dependency cache instead of exporting it again.
package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

const devWatchWorkspace = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"

// devWatchFixture is a Node app with a build script, which ADR-740 alone
// never live-patches (build_command). Its dev script is node --watch, the
// smallest development server that reloads edited files without help. Like
// Vite, it listens on 127.0.0.1:5173 unless given --host and refuses Host
// headers other than IP literals, localhost and
// __VITE_ADDITIONAL_SERVER_ALLOWED_HOSTS.
func devWatchFixture(t *testing.T, body string) []byte {
	t.Helper()
	const pkgJSON = `{"name":"faas-dev-watch","version":"1.0.0","private":true,"engines":{"node":"22"},
"scripts":{"build":"node -e \"require('fs').writeFileSync('build-ran.txt','yes')\"","start":"node index.js","dev":"node --watch index.js"},
"dependencies":{}}
`
	indexJS := `const http = require('http');
const hostFlag = process.argv.indexOf('--host');
const listenHost = hostFlag >= 0 ? process.argv[hostFlag + 1] : '127.0.0.1';
const allowed = (process.env.__VITE_ADDITIONAL_SERVER_ALLOWED_HOSTS || '').split(',').filter(Boolean);
const hostAllowed = (header) => {
  const name = (header || '').replace(/:[0-9]+$/, '');
  return name === 'localhost' || /^[0-9.]+$/.test(name) || name.startsWith('[') || allowed.includes(name);
};
http.createServer((req, res) => {
  if (!hostAllowed(req.headers.host)) {
    res.writeHead(403, {'content-type': 'text/plain'});
    res.end('Blocked request. This host is not allowed.\n');
    return;
  }
  res.writeHead(200, {'content-type': 'text/plain'});
  if (req.url === '/dev-host') {
    res.end((process.env.GREGALE_DEV_HOST || '') + '\n');
    return;
  }
  res.end(req.url === '/healthz' ? 'ready\n' : '` + body + `\n');
}).listen(5173, listenHost, () => console.log('dev watch fixture listening on ' + listenHost + ':5173'));
`
	return buildTarGz(t, map[string]string{"package.json": pkgJSON, "index.js": indexJS})
}

// devWatchGet probes the developer app with the account key: watch mode
// only runs behind the developer environment's authentication.
func devWatchGet(t *testing.T, h *e2etest.Harness, host, key string) (string, int) {
	t.Helper()
	return devWatchGetPath(t, h, host, key, "")
}

func devWatchGetPath(t *testing.T, h *e2etest.Harness, host, key, path string) (string, int) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, gatewayAppURL(h, "")+strings.TrimPrefix(path, "/"), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	req.Header.Set("Authorization", "Bearer "+key)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := h.HTTPClient().Do(req.WithContext(ctx))
	if err != nil {
		t.Fatalf("GET %s: %v", host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return strings.TrimSpace(string(body)), resp.StatusCode
}

func TestDevWatchMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" || os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_TEST_KERNEL / FAAS_BUILDER_BASE_PATH unset; skipping metal dev-watch test")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", builderImg))
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "dev watch base")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.All, "FAAS_DEV_PATCH_DELIVERY=1")
	defer h.DumpLogs(t)
	ctx := context.Background()
	key := h.SeedAccount(ctx, api.PlanPro)

	sessionBody, status := doReq(t, h, key, http.MethodPut, "/v1/dev/sessions/devwatch", api.UpsertDevSessionRequest{WorkspaceID: devWatchWorkspace})
	if status != http.StatusCreated {
		t.Fatalf("dev session: status %d body %s", status, sessionBody)
	}
	var session api.DevSessionResponse
	if err := json.Unmarshal(sessionBody, &session); err != nil {
		t.Fatal(err)
	}
	slug := session.App.Slug
	host := slug + ".apps.test.example"
	t.Cleanup(func() {
		if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil); status != http.StatusNoContent {
			t.Errorf("cleanup park: status %d", status)
		}
	})
	truthy := true
	if _, status := doReq(t, h, key, http.MethodPatch, "/v1/apps/"+slug, api.UpdateAppRequest{RequireAuthn: &truthy}); status != http.StatusOK {
		t.Fatalf("require authentication: status %d", status)
	}
	watchBody, status := doReq(t, h, key, http.MethodPut, "/v1/dev/sessions/devwatch", api.UpsertDevSessionRequest{
		WorkspaceID: devWatchWorkspace, Watch: &api.DevWatch{Command: "npm run dev"}})
	if status != http.StatusOK {
		t.Fatalf("enable watch mode: status %d body %s", status, watchBody)
	}

	first := postDevSource(t, h, key, slug, devWatchFixture(t, "dev watch v1"))
	t.Run("watch-build-live", func(t *testing.T) {
		wctx, cancel := context.WithTimeout(ctx, sourceDeployCtxTimeout())
		defer cancel()
		if _, _, err := e2etest.WaitForSourceDeployment(wctx, t, pool, first.ID, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling); err != nil {
			t.Fatalf("watch-mode sync did not go live: %v", err)
		}
		var verbatim, watch bool
		if err := pool.QueryRow(ctx, `select coalesce((p.dev_patch->>'verbatim')::boolean, false), coalesce((p.dev_patch->>'watch')::boolean, false)
			from build_provenance p join builds b on b.id = p.build_id where b.deployment_id = $1`, first.ID).Scan(&verbatim, &watch); err != nil {
			t.Fatalf("read build provenance: %v", err)
		}
		if !verbatim || !watch {
			t.Fatalf("build provenance verbatim=%t watch=%t, want a verbatim watch build of an app with a build script", verbatim, watch)
		}
		// Reaching a server on 127.0.0.1:5173 at all proves the runtime-port
		// bridge; 200 rather than 403 proves the gateway's Host reached a
		// server that was told to accept it.
		if body, code := devWatchGet(t, h, host, key); code != http.StatusOK || body != "dev watch v1" {
			t.Fatalf("app probe = %d %q, want 200 v1", code, body)
		}
		if body, code := devWatchGetPath(t, h, host, key, "/dev-host"); code != http.StatusOK || body != host {
			t.Fatalf("GREGALE_DEV_HOST = %d %q, want %q", code, body, host)
		}
	})

	second := postDevSource(t, h, key, slug, devWatchFixture(t, "dev watch v2"))
	t.Run("edit-hot-reloads", func(t *testing.T) {
		if second.DevPatch == nil || !second.DevPatch.Eligible || second.DevPatch.Generation < 1 {
			t.Fatalf("second sync dev_patch = %+v, want a published live patch", second.DevPatch)
		}
		statusPath := "/v1/dev/sessions/devwatch/patches/" + strconv.FormatInt(second.DevPatch.Generation, 10) + "?workspace_id=" + devWatchWorkspace
		deadline := time.Now().Add(60 * time.Second)
		for {
			body, code := doReq(t, h, key, http.MethodGet, statusPath, nil)
			var patch api.DevPatchStatusResponse
			if code == http.StatusOK && json.Unmarshal(body, &patch) == nil && patch.State != api.DevPatchStatePending {
				if patch.State != api.DevPatchStateApplied {
					t.Fatalf("patch status = %+v, want applied", patch)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("patch not applied within 60s (last status %d %s)", code, body)
			}
			time.Sleep(250 * time.Millisecond)
		}
		// node --watch reloads the edited file itself; guest-init must not
		// restart the workload in watch mode.
		deadline = time.Now().Add(30 * time.Second)
		for {
			body, code := devWatchGet(t, h, host, key)
			if code == http.StatusOK && body == "dev watch v2" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("running development server never served v2 (last %d %q)", code, body)
			}
			time.Sleep(250 * time.Millisecond)
		}
		var instances int
		if err := pool.QueryRow(ctx, `select count(*) from instances where app_id = $1 and deployment_id = $2 and state = 'running'`,
			session.App.ID, first.ID).Scan(&instances); err != nil || instances != 1 {
			t.Fatalf("running instances of the watch build = %d, %v; want the patched instance still serving", instances, err)
		}
	})
	t.Run("unchanged-dependencies-keep-cache", func(t *testing.T) {
		wctx, cancel := context.WithTimeout(ctx, sourceDeployCtxTimeout())
		defer cancel()
		if _, _, err := e2etest.WaitForSourceDeployment(wctx, t, pool, second.ID, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling); err != nil {
			t.Fatalf("second watch build did not go live: %v", err)
		}
		var buildLog string
		if err := pool.QueryRow(ctx, `select coalesce(string_agg(line, '' order by seq), '') from deployment_logs where deployment_id = $1 and stream = 'build'`,
			second.ID).Scan(&buildLog); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buildLog, "dependencies unchanged — kept the dependency cache") {
			t.Fatalf("second build did not keep the dependency cache; build log:\n%s", buildLog)
		}
		if body, code := devWatchGet(t, h, host, key); code != http.StatusOK || body != "dev watch v2" {
			t.Fatalf("second watch build probe = %d %q, want 200 v2", code, body)
		}
	})
}
