package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// newServer mounts the fixture's handler against an httptest.Server
// on 127.0.0.1. This is the fast path — every route exercise below
// uses it. The spawn test (TestSpawnedBinary_BootsAndServes) is the
// slower path that exercises the same binary Node/Python SDKs will
// spawn from their own CI.
func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	f := &fixture{}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return srv
}

// TestHealthz_OK: /__healthz returns 200 + {"ok":true}.
func TestHealthz_OK(t *testing.T) {
	srv := newServer(t)
	resp, err := http.Get(srv.URL + "/__healthz")
	if err != nil {
		t.Fatalf("GET /__healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type: got %q, want application/json prefix", ct)
	}
	var body map[string]bool
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body["ok"] {
		t.Errorf("body: got %+v, want {ok:true}", body)
	}
}

// TestAccount_OK: GET /v1/account returns the AccountResponse
// canonical shape.
func TestAccount_OK(t *testing.T) {
	srv := newServer(t)
	resp, err := http.Get(srv.URL + "/v1/account")
	if err != nil {
		t.Fatalf("GET /v1/account: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type: got %q, want application/json prefix", ct)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, k := range []string{"id", "email", "plan", "status", "limits", "usage_gb_hours", "app_count"} {
		if _, ok := body[k]; !ok {
			t.Errorf("missing required AccountResponse field %q in body: %+v", k, body)
		}
	}
	limits, ok := body["limits"].(map[string]any)
	if !ok {
		t.Fatalf("limits not an object: %+v", body["limits"])
	}
	for _, k := range []string{"plan", "ram_mb", "max_concurrency", "deployed_apps", "included_gb_hours", "app_layer_max_mb"} {
		if _, ok := limits[k]; !ok {
			t.Errorf("missing required AccountLimits field %q in limits: %+v", k, limits)
		}
	}
}

// Issue #311 — programmatic auth surface fixtures. The Gregale CLI
// uses these endpoints on the `gregale signup` interactive path. The
// fakeapid returns the canonical ProgrammaticAuthResponse shape so
// the CLI exercises the full unmarshaler + saveToken() path against
// the fixture. The plaintext is suffixed with the route so a test
// can assert which route was hit.
func TestV1AuthSignup_FixtureReturnsProgrammaticAuthResponse(t *testing.T) {
	srv := newServer(t)
	resp, err := http.Post(srv.URL+"/v1/auth/signup", "application/json",
		strings.NewReader(`{"email":"alice@example.com","password":"correct-horse-battery-staple"}`))
	if err != nil {
		t.Fatalf("POST /v1/auth/signup: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, k := range []string{"account_id", "plan", "api_key"} {
		if _, ok := body[k]; !ok {
			t.Errorf("missing ProgrammaticAuthResponse field %q: %+v", k, body)
		}
	}
	apiKey, ok := body["api_key"].(map[string]any)
	if !ok {
		t.Fatalf("api_key not an object: %+v", body["api_key"])
	}
	for _, k := range []string{"plaintext", "prefix", "id"} {
		if _, ok := apiKey[k]; !ok {
			t.Errorf("missing ProgrammaticAPIKey field %q: %+v", k, apiKey)
		}
	}
	plaintext, _ := apiKey["plaintext"].(string)
	if !strings.HasSuffix(plaintext, "_signup") {
		t.Errorf("api_key.plaintext = %q, want suffix \"_signup\"", plaintext)
	}
}

func TestV1AuthLogin_FixtureReturnsProgrammaticAuthResponse(t *testing.T) {
	srv := newServer(t)
	resp, err := http.Post(srv.URL+"/v1/auth/login", "application/json",
		strings.NewReader(`{"email":"alice@example.com","password":"correct-horse-battery-staple"}`))
	if err != nil {
		t.Fatalf("POST /v1/auth/login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	apiKey, ok := body["api_key"].(map[string]any)
	if !ok {
		t.Fatalf("api_key not an object: %+v", body["api_key"])
	}
	plaintext, _ := apiKey["plaintext"].(string)
	if !strings.HasSuffix(plaintext, "_login") {
		t.Errorf("api_key.plaintext = %q, want suffix \"_login\"", plaintext)
	}
}

func TestV1AuthSignupMagicLink_AlwaysReturns200OK(t *testing.T) {
	srv := newServer(t)
	// Every input — well-formed, malformed, missing — returns 200
	// with the same body. Anti-enumeration closure.
	for _, body := range []string{
		`{"email":"alice@example.com"}`,
		`{"email":"not-an-email"}`,
		`{}`,
		``,
	} {
		resp, err := http.Post(srv.URL+"/v1/auth/signup/magic-link", "application/json",
			strings.NewReader(body))
		if err != nil {
			t.Fatalf("POST /v1/auth/signup/magic-link body=%q: %v", body, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("body=%q status: got %d, want 200", body, resp.StatusCode)
		}
	}
}

// TestListApps_OK: GET /v1/apps returns an array of AppResponse.
func TestListApps_OK(t *testing.T) {
	srv := newServer(t)
	resp, err := http.Get(srv.URL + "/v1/apps")
	if err != nil {
		t.Fatalf("GET /v1/apps: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
	var body []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("expected at least one app in /v1/apps response, got empty array")
	}
	app := body[0]
	for _, k := range []string{
		"id", "slug", "type", "ram_mb", "max_concurrency", "concurrency_per_vm",
		"effective_limits", "min_instances", "status", "url", "manifest",
		"autoscale_target_rps", "autoscale_target_cpu_pct",
		"require_authn",
	} {
		if _, ok := app[k]; !ok {
			t.Errorf("missing required AppResponse field %q in app: %+v", k, app)
		}
	}
	// EgressAllowlist materialises as [] (never null) per
	// sdk/go/internal/api/dto.go:109 comment.
	if app["egress_allowlist"] == nil {
		t.Errorf("egress_allowlist should be materialised as [], got null")
	}
}

// TestCreateApp_OK: POST /v1/apps echoes the slug in the response.
// 201 Created matches api/openapi.yaml::paths./v1/apps.post.responses.201
// (the strict status code the Python generator decodes to AppResponse).
func TestCreateApp_OK(t *testing.T) {
	srv := newServer(t)
	body := strings.NewReader(`{"slug":"hello"}`)
	resp, err := http.Post(srv.URL+"/v1/apps", "application/json", body)
	if err != nil {
		t.Fatalf("POST /v1/apps: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("status: got %d, want 201", resp.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["slug"] != "hello" {
		t.Errorf("slug: got %v, want hello", got["slug"])
	}
	for _, k := range []string{
		"id", "type", "ram_mb", "max_concurrency", "concurrency_per_vm",
		"effective_limits", "min_instances", "status", "url", "manifest", "egress_allowlist",
		"autoscale_target_rps", "autoscale_target_cpu_pct",
		"require_authn",
	} {
		if _, ok := got[k]; !ok {
			t.Errorf("missing required AppResponse field %q in response: %+v", k, got)
		}
	}
}

// TestGetApp_OK: GET /v1/apps/hello returns the canonical app.
func TestGetApp_OK(t *testing.T) {
	srv := newServer(t)
	resp, err := http.Get(srv.URL + "/v1/apps/hello")
	if err != nil {
		t.Fatalf("GET /v1/apps/hello: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["slug"] != "hello" {
		t.Errorf("slug: got %v, want hello", got["slug"])
	}
}

// TestGetApp_NotFound: GET /v1/apps/missing-app-404 returns 404 +
// application/problem+json + code=not_found. This is the canonical
// sentinel the SDK's Unwrap matches.
func TestGetApp_NotFound(t *testing.T) {
	srv := newServer(t)
	resp, err := http.Get(srv.URL + "/v1/apps/missing-app-404")
	if err != nil {
		t.Fatalf("GET /v1/apps/missing-app-404: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type: got %q, want application/problem+json", ct)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["code"] != "not_found" {
		t.Errorf("code: got %v, want not_found", body["code"])
	}
	// Required RFC 7807 fields per api/openapi.yaml:2320.
	for _, k := range []string{"title", "status", "code"} {
		if _, ok := body[k]; !ok {
			t.Errorf("missing required Problem field %q: %+v", k, body)
		}
	}
	if int(body["status"].(float64)) != 404 {
		t.Errorf("status field: got %v, want 404", body["status"])
	}
}

// TestGetUsage_OK: GET /v1/usage returns an ARRAY of UsageResponse.
// Memory getusage-wire-shape-mismatch.md: the SDK decodes
// []UsageResponse, not a single struct. The smoke must round-trip
// the array path.
func TestGetUsage_OK(t *testing.T) {
	srv := newServer(t)
	resp, err := http.Get(srv.URL + "/v1/usage?month=2026-07")
	if err != nil {
		t.Fatalf("GET /v1/usage: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
	var body []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("expected at least one usage row, got empty array")
	}
	row := body[0]
	for _, k := range []string{"app_id", "mb_seconds", "requests", "included_gb_hours"} {
		if _, ok := row[k]; !ok {
			t.Errorf("missing required UsageResponse field %q in row: %+v", k, row)
		}
	}
}

// TestUnknownSlug_404Problem: an unknown slug (but not the
// sentinel) still returns 404 + problem+json. The detail includes
// the slug.
func TestUnknownSlug_404Problem(t *testing.T) {
	srv := newServer(t)
	resp, err := http.Get(srv.URL + "/v1/apps/no-such-app")
	if err != nil {
		t.Fatalf("GET /v1/apps/no-such-app: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["code"] != "not_found" {
		t.Errorf("code: got %v, want not_found", body["code"])
	}
	detail, _ := body["detail"].(string)
	if !strings.Contains(detail, "no-such-app") {
		t.Errorf("detail should mention slug, got %q", detail)
	}
}

// TestAuth_Permissive: missing Authorization header still returns
// 200. The fixture is permissive by design (per PR 4 plan).
func TestAuth_Permissive(t *testing.T) {
	srv := newServer(t)
	// No Authorization header at all.
	resp, err := http.Get(srv.URL + "/v1/account")
	if err != nil {
		t.Fatalf("GET /v1/account: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200 (permissive auth)", resp.StatusCode)
	}
}

// freePort asks the kernel for a free TCP port. We close the
// listener immediately and return the port; there's a TOCTOU race
// here (the port could be taken between Close and the subprocess
// binding), but in practice this is fine for CI which is the only
// place we exercise the spawn flow.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// buildFixtureBinary compiles ../fakeapid to a temp path. Used by
// the spawn test below; no-op if the binary already exists at
// ./bin/fakeapid and is newer than main.go.
func buildFixtureBinary(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tmp := filepath.Join(t.TempDir(), "fakeapid")
	cmd := exec.Command("go", "build", "-o", tmp, ".")
	cmd.Dir = wd
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build: %v\n%s", err, buf.String())
	}
	return tmp
}

// spawnFakeAPID starts the binary on a free port, polls /__healthz
// until ready, and returns the base URL + a stop func that kills
// the subprocess. Setpgid ensures the binary survives a t.Fatalf
// in the parent (memory: e2e-harness-daemon-leak.md).
func spawnFakeAPID(t *testing.T, binPath string) (string, func()) {
	t.Helper()
	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, binPath)
	cmd.Env = append(os.Environ(), "PORT="+strconv.Itoa(port))
	// Capture stdout/stderr in a mutex-guarded buffer (the daemon
	// may log a startup line and we want to surface it on failure
	// without racing the writer under -race).
	logBuf := &safeBuffer{}
	cmd.Stdout = logBuf
	cmd.Stderr = logBuf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start fakeapid: %v", err)
	}
	base := "http://127.0.0.1:" + strconv.Itoa(port)

	// Poll /__healthz with a short timeout. The binary prints
	// "fakeapid listening on ..." before serving, so 5s is plenty
	// on CI runners.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/__healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return base, func() {
					// Kill the whole process group; Setpgid:true
					// means a Kill of the leader is enough.
					_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
					_ = cmd.Wait()
					cancel()
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Failed to become healthy. Dump captured log to help debug.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Wait()
	cancel()
	t.Fatalf("fakeapid did not become healthy on %s within 5s\nlog:\n%s", base, logBuf.String())
	return "", nil // unreachable
}

// TestSpawnedBinary_BootsAndServes proves the compiled binary
// (which Node + Python SDKs will spawn from their own CI) actually
// boots and serves the canonical routes. Without this, the SDK
// smoke tests would pass against an in-process server but fail
// against the shipped artifact.
func TestSpawnedBinary_BootsAndServes(t *testing.T) {
	bin := buildFixtureBinary(t)
	base, stop := spawnFakeAPID(t, bin)
	defer stop()

	// Hit /v1/account and assert canonical body shape.
	resp, err := http.Get(base + "/v1/account")
	if err != nil {
		t.Fatalf("GET /v1/account: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/v1/account status: got %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["plan"] != "hobby" {
		t.Errorf("plan: got %v, want hobby", body["plan"])
	}

	// Hit /v1/apps/missing-app-404 and assert the Problem
	// envelope round-trips through the wire.
	resp2, err := http.Get(base + "/v1/apps/missing-app-404")
	if err != nil {
		t.Fatalf("GET /v1/apps/missing-app-404: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", resp2.StatusCode)
	}
	if ct := resp2.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type: got %q, want application/problem+json", ct)
	}
}

// safeBuffer is a mutex-guarded bytes.Buffer. Captures cmd output
// without tripping -race when the smoke test reads the buffer
// while the daemon writes (memory: e2etest-harness-safebuffer).
// Renamed from syncBuffer for parity with pkg/e2etest's
// e2etest.SafeBuffer — the two helpers have identical semantics
// and a single name across the repo makes grep cheaper.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
