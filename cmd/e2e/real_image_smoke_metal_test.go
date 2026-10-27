//go:build metal

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

// realImageCase is one public image deployed as a customer would deploy it.
type realImageCase struct {
	slug       string
	image      string
	entrypoint []string
	cmd        []string
	port       int
	// check validates the body the app served through the gateway.
	check func(t *testing.T, body []byte)
}

// pythonRuntimeProbe serves what the guest gives a container
// (ADR-20261025111339): it creates POSIX shared memory, opens a pty, and
// reports the hostname and its resolution, the descriptor limit, HOME, PATH
// and the working directory.
const pythonRuntimeProbe = `
import http.server, json, os, resource, socket
from multiprocessing import shared_memory
shm = shared_memory.SharedMemory(create=True, size=32 << 20)
shm.close(); shm.unlink()
master, slave = os.openpty(); os.close(master); os.close(slave)
info = {
    "hostname": socket.gethostname(),
    "localhost": socket.gethostbyname("localhost"),
    "self": socket.gethostbyname(socket.gethostname()),
    "nofile": resource.getrlimit(resource.RLIMIT_NOFILE),
    "home": os.environ.get("HOME", ""),
    "path": os.environ.get("PATH", ""),
    "cwd": os.getcwd(),
}
body = json.dumps(info).encode()
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200); self.send_header("Content-Type", "application/json"); self.end_headers(); self.wfile.write(body)
http.server.HTTPServer(("0.0.0.0", int(os.environ["PORT"])), H).serve_forever()
`

// javaLocalHost serves InetAddress.getLocalHost(), which threw
// UnknownHostException on a guest named "(none)".
const javaLocalHost = `mkdir -p /tmp/app && cat > /tmp/app/Main.java <<'EOF'
import com.sun.net.httpserver.HttpServer;
import java.net.InetAddress;
import java.net.InetSocketAddress;
public class Main {
  public static void main(String[] args) throws Exception {
    String host = InetAddress.getLocalHost().toString();
    HttpServer s = HttpServer.create(new InetSocketAddress(Integer.parseInt(System.getenv("PORT"))), 0);
    s.createContext("/", x -> { byte[] b = ("java-ok " + host).getBytes(); x.sendResponseHeaders(200, b.length); x.getResponseBody().write(b); x.close(); });
    s.start();
  }
}
EOF
exec java /tmp/app/Main.java`

// chromiumDumpDOM renders a page with headless Chromium, which needs
// /dev/shm, and serves the DOM it produced, or Chromium's error output when
// it failed, with busybox nc (the image's busybox has no httpd).
const chromiumDumpDOM = `mkdir -p /tmp/www
chromium-browser --headless --no-sandbox --disable-gpu --dump-dom 'data:text/html,<p>gregale-chromium-ok</p>' > /tmp/www/index.html 2> /tmp/www/chromium.log || cat /tmp/www/chromium.log >> /tmp/www/index.html
while true; do { printf 'HTTP/1.0 200 OK\r\nContent-Type: text/html\r\n\r\n'; cat /tmp/www/index.html; } | nc -l -p "$PORT"; done`

func realImageCases() []realImageCase {
	return []realImageCase{
		{slug: "real-nginx", image: "docker.io/library/nginx:1.27-alpine", check: func(t *testing.T, body []byte) {
			if !strings.Contains(string(body), "Welcome to nginx") {
				t.Fatalf("nginx body = %q", body)
			}
		}},
		{slug: "real-python-runtime", image: "docker.io/library/python:3.12-alpine",
			entrypoint: []string{"python", "-c", pythonRuntimeProbe}, port: 8080, check: checkPythonRuntime},
		{slug: "real-java", image: "docker.io/library/eclipse-temurin:21-jdk-alpine",
			entrypoint: []string{"sh", "-c", javaLocalHost}, port: 8080, check: func(t *testing.T, body []byte) {
				if !strings.HasPrefix(string(body), "java-ok ") {
					t.Fatalf("java body = %q", body)
				}
			}},
		{slug: "real-chromium", image: "docker.io/zenika/alpine-chrome:124",
			entrypoint: []string{"sh", "-c", chromiumDumpDOM}, port: 8080, check: func(t *testing.T, body []byte) {
				if !strings.Contains(string(body), "gregale-chromium-ok") {
					t.Fatalf("chromium did not render the page: %q", body)
				}
			}},
	}
}

func checkPythonRuntime(t *testing.T, body []byte) {
	t.Helper()
	var got struct {
		Hostname  string `json:"hostname"`
		Localhost string `json:"localhost"`
		Self      string `json:"self"`
		NoFile    []int  `json:"nofile"`
		Home      string `json:"home"`
		Path      string `json:"path"`
		Cwd       string `json:"cwd"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("runtime probe body %q: %v", body, err)
	}
	if got.Hostname == "" || got.Hostname == "(none)" || got.Localhost != "127.0.0.1" || got.Self == "" {
		t.Errorf("hostname resolution: %+v", got)
	}
	if len(got.NoFile) != 2 || got.NoFile[0] < api.GuestOpenFilesSoftLimit || got.NoFile[1] < api.GuestOpenFilesHardLimit {
		t.Errorf("RLIMIT_NOFILE = %v, want at least %d/%d", got.NoFile, api.GuestOpenFilesSoftLimit, api.GuestOpenFilesHardLimit)
	}
	if got.Home == "" || got.Home == "/" || !strings.Contains(got.Path, "/usr/local/bin") {
		t.Errorf("HOME/PATH: %+v", got)
	}
	t.Logf("python runtime probe: %s", body)
}

// TestRealImageSmokeMetal deploys public images from real registries, the
// way customers deploy them, and checks each serves through the gateway and
// wakes from its snapshot: nginx, a Python probe of the guest's container
// environment, Java's InetAddress.getLocalHost and headless Chromium. Docker
// Hub pulls go through mirror.gcr.io first (ADR-20261025114554).
//
// It needs registry access and runs only with FAAS_E2E_REAL_IMAGES=1. It is
// not part of the hermetic containers lane (docs/container-qualification.md).
func TestRealImageSmokeMetal(t *testing.T) {
	if os.Getenv("FAAS_E2E_REAL_IMAGES") != "1" {
		t.Skip("FAAS_E2E_REAL_IMAGES unset; this smoke test pulls public images")
	}
	if os.Getenv("FAAS_TEST_KERNEL") == "" || os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_TEST_KERNEL or FAAS_BUILDER_BASE_PATH unset")
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
	// Real registries need imaged's HTTPS egress client, not the harness's
	// plain-HTTP fake-registry mode.
	h := e2etest.Start(t, pool, e2etest.DeployWake, "FAAS_OCI_INSECURE=0", api.DockerHubMirrorsEnv+"=mirror.gcr.io")
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	key := h.SeedAccount(context.Background(), api.PlanScale)
	for _, tc := range realImageCases() {
		t.Run(tc.slug, func(t *testing.T) { runRealImageCase(t, h, pool, key, tc) })
	}
}

// pinImageDigest resolves a tag to the digest deploys require, through the
// same Docker Hub mirror imaged uses.
func pinImageDigest(t *testing.T, ref string) string {
	t.Helper()
	parsed, err := oci.ParseReference(ref)
	if err != nil {
		t.Fatal(err)
	}
	client := oci.NewRegistryClient(oci.WithHTTPClient(oci.NewEgressHTTPClient()), oci.WithDockerHubMirrors([]string{"mirror.gcr.io"}, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	digest, err := client.PullDigest(ctx, ref)
	if err != nil {
		t.Fatalf("resolve %s: %v", ref, err)
	}
	return parsed.Registry + "/" + parsed.Repository + "@" + digest
}

func runRealImageCase(t *testing.T, h *e2etest.Harness, pool *pgxpool.Pool, key string, tc realImageCase) {
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: tc.slug, Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, tc.slug)
	// Scale defaults to bearer public auth (ADR-079); the anonymous GETs
	// below check the workload, not edge auth.
	if body, code := doReq(t, h, key, http.MethodPatch, "/v1/apps/"+tc.slug, map[string]any{
		"require_authn": false, "public_auth": map[string]any{"mode": "open"},
	}); code != http.StatusOK {
		t.Fatalf("open public auth: %d %s", code, body)
	}
	request := api.CreateDeploymentRequest{Image: pinImageDigest(t, tc.image)}
	if tc.entrypoint != nil || tc.cmd != nil || tc.port != 0 {
		request.Overrides = &api.CreateDeploymentOverrides{Entrypoint: tc.entrypoint, Cmd: tc.cmd, Port: tc.port}
	}
	body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+tc.slug+"/deployments", request)
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, body)
	}
	depID := parseImageDeployment(t, body)
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, depID, 19*time.Minute); err != nil {
		t.Fatalf("%s did not reach live: %v", tc.image, err)
	}
	t.Logf("%s live after %s", tc.image, time.Since(started).Round(time.Second))
	host := tc.slug + ".apps.test.example"
	tc.check(t, getOK(t, h, host, "GET"))

	// Scale to zero by idle timeout, as customers' apps do, and wake from the
	// snapshot: the environment must survive a restore, not only a cold boot.
	setAppIdleTimeout(t, h, key, tc.slug, api.IdleTimeoutFloorSeconds)
	parkCtx, parkCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer parkCancel()
	if _, err := e2etest.WaitForInstanceState(parkCtx, t, pool, appID, state.StateParked, 290*time.Second); err != nil {
		t.Fatalf("did not scale to zero: %v", err)
	}
	woke := time.Now()
	tc.check(t, getOK(t, h, host, "wake GET"))
	t.Logf("%s served after waking in %s", tc.image, time.Since(woke).Round(time.Millisecond))
	if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+tc.slug+"/park", nil); status != http.StatusNoContent {
		t.Fatalf("cleanup park: status=%d", status)
	}
}

// getOK GETs the app through the gateway until it answers 200, retrying a
// gateway 502 for up to 30 s: busybox nc serves one connection at a time.
func getOK(t *testing.T, h *e2etest.Harness, host, what string) []byte {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		body, status := doGetWithHost(t, h.HTTPClient(), gatewayAppURL(h, ""), host, 2*time.Minute)
		if status == http.StatusOK {
			return body
		}
		if status != http.StatusBadGateway || time.Now().After(deadline) {
			t.Fatalf("%s: status=%d body=%s", what, status, body)
		}
		time.Sleep(time.Second)
	}
}
