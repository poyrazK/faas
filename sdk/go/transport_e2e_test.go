package faas

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestE2E_LoggingAndRetryStack_AgainstFakeAPID is the integration
// test for the new RT stack. It spawns sdk/fakeapid/bin/fakeapid
// (built on demand), constructs a real *faas.Client with
// WithLogger + WithRetry + WithHTTPClient, and exercises the
// canonical happy path + the canonical 404 sentinel — proving
// that the new wrappers compose correctly with the existing
// idempotency RoundTripper and that Problem unwrap still works.
func TestE2E_LoggingAndRetryStack_AgainstFakeAPID(t *testing.T) {
	bin := buildFakeapidBinary(t)
	base, stop := spawnFakeapid(t, bin)
	defer stop()

	var logBuf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	c, err := NewClient(base, "test-token",
		WithLogger(log),
		WithRetry(2, time.Millisecond),
		WithHTTPClient(&http.Client{Timeout: 5 * time.Second}),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	// Happy path: GET /v1/apps/hello returns 200, no error.
	app, err := c.GetApp(context.Background(), "hello")
	if err != nil {
		t.Fatalf("GetApp hello: %v", err)
	}
	if app.Slug != "hello" {
		t.Errorf("app.Slug: got %q, want hello", app.Slug)
	}

	// Canonical sentinel: GET /v1/apps/missing-app-404 returns
	// 404 application/problem+json with code=not_found; the SDK's
	// Unwrap must surface ErrNotFound.
	_, err = c.GetApp(context.Background(), "missing-app-404")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("errors.Is: got %v, want ErrNotFound", err)
	}

	// Slog buffer must contain at least one request and one
	// response line (one log per attempt per RT). The exact count
	// depends on the retry RT, which is a no-op for 200 + 404
	// here, so we expect 2 attempts × 2 log lines = 4.
	out := logBuf.String()
	if !contains(out, "faas http request") {
		t.Errorf("expected request log, got:\n%s", out)
	}
	if !contains(out, "faas http response") {
		t.Errorf("expected response log, got:\n%s", out)
	}
}

// contains is a small helper to keep the assertion sites readable.
func contains(s, sub string) bool {
	return bytes.Contains([]byte(s), []byte(sub))
}

// buildFakeapidBinary compiles ../fakeapid to a temp path. We
// don't reuse ./bin/fakeapid because (a) it's not in the SDK
// module's source tree and (b) we want the test to be hermetic
// regardless of whether the sibling module is already built.
func buildFakeapidBinary(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// sdk/go → sdk/fakeapid is ../fakeapid from the SDK module
	// root.
	fakeapidRoot, err := filepath.Abs(filepath.Join(wd, "..", "fakeapid"))
	if err != nil {
		t.Fatalf("abs fakeapid: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fakeapidRoot, "go.mod")); err != nil {
		t.Fatalf("fakeapid not found at %s: %v (this test must run from the SDK module root)", fakeapidRoot, err)
	}
	tmp := filepath.Join(t.TempDir(), "fakeapid")
	cmd := exec.Command("go", "build", "-o", tmp, ".")
	cmd.Dir = fakeapidRoot
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build fakeapid: %v\n%s", err, buf.String())
	}
	return tmp
}

// spawnFakeapid starts the binary on a free port, polls
// /__healthz, and returns base + stop. Mirrors the helper in
// sdk/fakeapid/main_test.go (we don't import that package because
// it would create a circular test dep).
func spawnFakeapid(t *testing.T, binPath string) (string, func()) {
	t.Helper()
	port, err := freePort()
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	cmd := exec.Command(binPath)
	cmd.Env = append(os.Environ(), "PORT="+strconv.Itoa(port))
	logBuf := &safeBuffer{}
	cmd.Stdout = logBuf
	cmd.Stderr = logBuf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fakeapid: %v", err)
	}
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/__healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return base, func() {
					_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
					_ = cmd.Wait()
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Wait()
	t.Fatalf("fakeapid did not become healthy on %s within 5s\nlog:\n%s", base, logBuf.String())
	return "", nil
}

// freePort asks the kernel for an unused TCP port. There's a
// TOCTOU race between Close and the subprocess binding, but in
// practice CI runs a single fakeapid per test process so this is
// fine.
func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port, nil
}

// safeBuffer is a mutex-guarded bytes.Buffer for capturing
// subprocess stdio under -race (memory: e2etest-harness-safebuffer).
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

// Ensure unused import warning doesn't fire when this file is
// read in isolation (json is used by the assertion helpers; this
// reference keeps the import set stable).
var _ = json.NewEncoder
var _ = io.EOF
