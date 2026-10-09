//go:build metal

// dev_loop_metal_test.go — ADR-740 developer live patches and ADR-741
// debugger attach on a real Firecracker stack.
//
//	PUT /v1/dev/sessions  → developer app
//	dev-source sync #1    → builder VM classifies the Railpack plan → live
//	debugger tunnel       → gateway → vmmd ForwardTCPStream → Node inspector
//	dev-source sync #2    → apid publishes a cumulative patch → guest-init
//	                        applies it in the running VM → new body served
//	                        before the second build is live
//	park                  → vmmd refuses to snapshot the patched VM
//	                        (dev_source_diverged), no new snapshot row
//	second build live     → new body served from the real artifact
package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/sourcedelta"
	"github.com/onebox-faas/faas/pkg/state"
)

const devLoopWorkspace = "dddddddddddddddddddddddddddddddd"

// devLoopFixture is a Node app without a build script, so Railpack copies its
// source into /app unchanged and edits to index.js are live-patchable.
func devLoopFixture(t *testing.T, body string) []byte {
	t.Helper()
	const pkgJSON = `{"name":"faas-dev-loop","version":"1.0.0","private":true,"engines":{"node":"22"},"scripts":{"start":"node index.js"},"dependencies":{}}
`
	indexJS := `const http = require('http');
http.createServer((req, res) => {
  res.writeHead(200, {'content-type': 'text/plain'});
  res.end(req.url === '/healthz' ? 'ready\n' : '` + body + `\n');
}).listen(3000, () => console.log('dev loop fixture listening on :3000'));
`
	return buildTarGz(t, map[string]string{"package.json": pkgJSON, "index.js": indexJS})
}

func devSourceRevision(t *testing.T, archive []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.tar.gz")
	if err := os.WriteFile(path, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	manifest, err := sourcedelta.Inspect(f, sourcedelta.Limits{MaxEntries: api.SourceArchiveMaxEntries})
	if err != nil {
		t.Fatal(err)
	}
	return manifest.Revision
}

// postDevSource uploads a complete developer source snapshot.
func postDevSource(t *testing.T, h *e2etest.Harness, key, slug string, archive []byte) api.DeploymentResponse {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("source", "src.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(archive); err != nil {
		t.Fatal(err)
	}
	if err := mw.WriteField("dev_source_target", devSourceRevision(t, archive)); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/v1/apps/%s/deployments/dev-source", h.APIDURL, slug), &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Idempotency-Key", fmt.Sprintf("dev-loop-%d", time.Now().UnixNano()))
	resp, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var dep api.DeploymentResponse
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("dev-source upload: status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&dep); err != nil {
		t.Fatal(err)
	}
	return dep
}

func devLoopBody(t *testing.T, h *e2etest.Harness, host string) string {
	t.Helper()
	body, status := doGetWithHost(t, h.HTTPClient(), gatewayAppURL(h, ""), host, 30*time.Second)
	if status != http.StatusOK {
		t.Fatalf("app probe: status %d body %q", status, body)
	}
	return strings.TrimSpace(string(body))
}

func TestDevLoopMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" || os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_TEST_KERNEL / FAAS_BUILDER_BASE_PATH unset; skipping metal dev-loop test")
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
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "dev loop base")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.All, "FAAS_DEV_PATCH_DELIVERY=1")
	defer h.DumpLogs(t)
	ctx := context.Background()
	key := h.SeedAccount(ctx, api.PlanPro)

	sessionBody, status := doReq(t, h, key, http.MethodPut, "/v1/dev/sessions/devloop", api.UpsertDevSessionRequest{WorkspaceID: devLoopWorkspace})
	if status != http.StatusCreated {
		t.Fatalf("dev session: status %d body %s", status, sessionBody)
	}
	var session api.DevSessionResponse
	if err := json.Unmarshal(sessionBody, &session); err != nil {
		t.Fatal(err)
	}
	slug := session.App.Slug
	host := slug + ".apps.test.example"
	falsy := false
	if _, status := doReq(t, h, key, http.MethodPatch, "/v1/apps/"+slug, api.UpdateAppRequest{RequireAuthn: &falsy}); status != http.StatusOK {
		t.Fatalf("disable require_authn: status %d", status)
	}
	// ADR-741: what `gregale dev --debug` sets before the first sync.
	if _, status := doReq(t, h, key, http.MethodPut, "/v1/apps/"+slug+"/secrets/"+api.DevDebugEnv, api.PutAppSecretRequest{Value: api.DevDebugRuntimeNode}); status/100 != 2 {
		t.Fatalf("set debug secret: status %d", status)
	}

	first := postDevSource(t, h, key, slug, devLoopFixture(t, "dev loop v1"))
	t.Run("first-sync-live", func(t *testing.T) {
		wctx, cancel := context.WithTimeout(ctx, sourceDeployCtxTimeout())
		defer cancel()
		if _, _, err := e2etest.WaitForSourceDeployment(wctx, t, pool, first.ID, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling); err != nil {
			t.Fatalf("first sync did not go live: %v", err)
		}
		var verbatim bool
		if err := pool.QueryRow(ctx, `select coalesce((p.dev_patch->>'verbatim')::boolean, false)
			from build_provenance p join builds b on b.id = p.build_id where b.deployment_id = $1`, first.ID).Scan(&verbatim); err != nil {
			t.Fatalf("read build provenance: %v", err)
		}
		if !verbatim {
			t.Fatal("builder did not classify the no-build-script Node app as verbatim")
		}
		if got := devLoopBody(t, h, host); got != "dev loop v1" {
			t.Fatalf("body = %q, want v1", got)
		}
	})

	t.Run("debugger-tunnel", func(t *testing.T) {
		url := "ws" + strings.TrimPrefix(h.GatewayURL, "http") + "/v1/apps/" + slug + "/debug"
		socket, resp, err := (&websocket.Dialer{HandshakeTimeout: 60 * time.Second}).Dial(url, http.Header{"Authorization": {"Bearer " + key}})
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if err != nil {
			t.Fatalf("debug tunnel dial: %v", err)
		}
		defer func() { _ = socket.Close() }()
		if err := socket.WriteMessage(websocket.BinaryMessage, []byte("GET /json/version HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n")); err != nil {
			t.Fatal(err)
		}
		_ = socket.SetReadDeadline(time.Now().Add(30 * time.Second))
		var reply bytes.Buffer
		for !strings.Contains(reply.String(), "Protocol-Version") {
			_, chunk, err := socket.ReadMessage()
			if err != nil {
				t.Fatalf("inspector reply %q: %v", reply.String(), err)
			}
			reply.Write(chunk)
		}
		if !strings.Contains(reply.String(), "node.js") && !strings.Contains(reply.String(), "Node.js") {
			t.Fatalf("inspector reply = %q, want the Node.js version document", reply.String())
		}
	})

	var patchedInstance string
	second := postDevSource(t, h, key, slug, devLoopFixture(t, "dev loop v2"))
	t.Run("live-patch-applied", func(t *testing.T) {
		if second.DevPatch == nil || !second.DevPatch.Eligible || second.DevPatch.Generation < 1 {
			t.Fatalf("second sync dev_patch = %+v, want an eligible published patch", second.DevPatch)
		}
		statusPath := fmt.Sprintf("/v1/dev/sessions/devloop/patches/%d?workspace_id=%s", second.DevPatch.Generation, devLoopWorkspace)
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
		// The restarted process serves the edit from the patched VM.
		deadline = time.Now().Add(30 * time.Second)
		for devLoopBody(t, h, host) != "dev loop v2" {
			if time.Now().After(deadline) {
				t.Fatal("patched instance never served v2")
			}
			time.Sleep(250 * time.Millisecond)
		}
		if err := pool.QueryRow(ctx, `select id from instances where app_id = $1 and deployment_id = $2 and state = 'running' limit 1`,
			session.App.ID, first.ID).Scan(&patchedInstance); err != nil {
			t.Fatalf("patched instance: %v", err)
		}
	})

	t.Run("patched-instance-not-snapshotted", func(t *testing.T) {
		if patchedInstance == "" {
			t.Skip("no patched instance")
		}
		var before int
		if err := pool.QueryRow(ctx, `select count(*) from snapshots where deployment_id = $1`, first.ID).Scan(&before); err != nil {
			t.Fatalf("count snapshots: %v", err)
		}
		if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil); status != http.StatusNoContent {
			t.Fatalf("park: status %d", status)
		}
		deadline := time.Now().Add(60 * time.Second)
		for {
			var reason string
			err := pool.QueryRow(ctx, `select data->>'reason' from events
				where kind = 'wake.park_failed' and data->>'instance_id' = $1 order by at desc limit 1`, patchedInstance).Scan(&reason)
			if err == nil {
				if reason != api.CodeDevSourceDiverged {
					t.Fatalf("park_failed reason = %q, want %s", reason, api.CodeDevSourceDiverged)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("no park_failed event for the patched instance: %v", err)
			}
			time.Sleep(500 * time.Millisecond)
		}
		var after int
		if err := pool.QueryRow(ctx, `select count(*) from snapshots where deployment_id = $1`, first.ID).Scan(&after); err != nil {
			t.Fatalf("count snapshots: %v", err)
		}
		if after != before {
			t.Fatalf("snapshots for the patched deployment went %d → %d; a patched VM was captured", before, after)
		}
	})

	t.Run("second-build-serves-the-edit", func(t *testing.T) {
		wctx, cancel := context.WithTimeout(ctx, sourceDeployCtxTimeout())
		defer cancel()
		dep, _, err := e2etest.WaitForSourceDeployment(wctx, t, pool, second.ID, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling)
		if err != nil {
			t.Fatalf("second sync did not go live: %v", err)
		}
		if dep.Status != state.DeployLive {
			t.Fatalf("second deployment status %s", dep.Status)
		}
		if got := devLoopBody(t, h, host); got != "dev loop v2" {
			t.Fatalf("body from the built artifact = %q, want v2", got)
		}
	})
}
