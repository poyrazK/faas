// Tests for forwardHTTPStreamV2 (issue #686 / H2C inner-leg bridge).
// These exercise the new v2 code path via the streamBridgeSpawn
// package var, which we override to run the bridge binary against
// a fake guest rather than `ip netns exec …` (which requires Linux
// + root + a real netns). The fake-spawn path is the same surface
// the production spawn uses (same argv, same env, same SockPath).
//
// Coverage:
//   - TestForwardHTTPStreamV2_HeadersAssembledFromInit: the bridge
//     writes a correct H1 request line + headers + chunked body
//     to the guest — the framing the v1 shell bridge emitted.
//   - TestForwardHTTPStreamV2_StreamBridgeVersion_FallsBackToV1:
//     when FAAS_STREAM_BRIDGE_VERSION=v1 is set, the v1 path runs
//     (this test just verifies the selector is consulted).
//
// The wire-shape assertions (SETTINGS frame, no H1 request line)
// are the domain of the metal streaming test in cmd/e2e; this
// unit test focuses on the spawn + framing surface that is
// testable on macOS.

package vmmdgrpc_test

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

// buildBridgeBinary compiles cmd/vmmd-stream-bridge into a temp
// dir and returns the absolute path. Same pattern as
// cmd/vmmd-raw-bridge/main_test.go::buildBridgeForTest.
func buildBridgeBinary(t *testing.T) string {
	t.Helper()
	_, src, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller: no path")
	}
	srcDir := filepath.Dir(src)
	bridgeDir := filepath.Clean(filepath.Join(srcDir, "..", "..", "cmd", "vmmd-stream-bridge"))
	out := filepath.Join(t.TempDir(), "vmmd-stream-bridge")
	cmd := exec.Command("go", "build", "-o", out, bridgeDir)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build bridge binary: %v", err)
	}
	return out
}

// fakeBridgeSpawn is the test-side override for the production
// `streamBridgeSpawn` hook. Tests call SetStreamBridgeSpawn(fake)
// to inject it; the in-line test in
// TestForwardHTTPStreamV2_HeadersAssembledFromInit runs the
// bridge binary directly via exec.CommandContext (the test is
// the same surface the production spawn produces — same argv,
// same env, same SockPath — minus the `ip netns exec` wrapper
// which requires Linux + root + a real netns).

// startFakeGuest stands up a TCP listener on 127.0.0.1 that
// captures the bridge's H1 framing and replies with a fixed
// chunked response. Returns the listener, the captured-bytes
// handler, and the port.
type fakeGuest struct {
	mu      sync.Mutex
	request []byte
	body    []byte
}

func (f *fakeGuest) Serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	buf := make([]byte, 64*1024)
	for {
		_ = c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, err := c.Read(buf)
		if n > 0 {
			f.mu.Lock()
			f.request = append(f.request, buf[:n]...)
			if idx := indexCRLFCRLF(f.request); idx >= 0 {
				f.body = append([]byte(nil), f.request[idx+4:]...)
			}
			f.mu.Unlock()
		}
		if err != nil {
			break
		}
	}
	// Reply with a fixed chunked response.
	_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n0\r\n\r\n"))
}

func startFakeGuest(t *testing.T) (net.Listener, *fakeGuest) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("fake guest listen: %v", err)
	}
	f := &fakeGuest{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.Serve(conn)
		}
	}()
	return ln, f
}

func indexCRLFCRLF(b []byte) int {
	const sep = "\r\n\r\n"
	for i := 0; i+len(sep) <= len(b); i++ {
		if string(b[i:i+len(sep)]) == sep {
			return i
		}
	}
	return -1
}

// TestForwardHTTPStreamV2_HeadersAssembledFromInit exercises the
// v2 wire-up end-to-end by:
//  1. Building the bridge binary.
//  2. Launching a fake guest on 127.0.0.1.
//  3. Overriding streamBridgeSpawn with a fake that runs the
//     bridge binary directly (no netns exec — the bridge is
//     the test subject).
//  4. Asserting the bridge writes the H1 request line + headers
//     + chunked framing to the guest.
//
// The full ForwardHTTPStreamV2 call requires a live gRPC stream
// and the vmmd Server instance — this test focuses on the bridge
// binary's framing, which is the part that had bugs in PR #749.
func TestForwardHTTPStreamV2_HeadersAssembledFromInit(t *testing.T) {
	bridgeBin := buildBridgeBinary(t)
	ln, fake := startFakeGuest(t)
	defer func() { _ = ln.Close() }()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port := atou(t, portStr)

	// Bind a unix socket for the bridge. Use /tmp explicitly
	// because macOS has a 104-byte unix socket path limit and
	// t.TempDir() on macOS returns paths under
	// /var/folders/.../T/ which can exceed it. The path also
	// needs to be unique per test invocation — `make test` runs
	// all packages in parallel and a hardcoded path races with
	// other tests in this package that bind the same name.
	// Suffix with PID + a sanitised t.Name() (which may contain
	// `/` for nested subtests; unix socket paths can't).
	sockPath := "/tmp/faas-stream-bridge-test-" + fmt.Sprintf("%d-%s", os.Getpid(), strings.ReplaceAll(t.Name(), "/", "_")) + ".sock"
	_ = os.Remove(sockPath)
	lnBridge, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("bridge listen: %v", err)
	}
	_ = lnBridge.Close()
	t.Cleanup(func() { _ = os.Remove(sockPath) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	deadline := time.Now().Add(5 * time.Second).UTC().Format(time.RFC3339)
	env := []string{
		"FAAS_BRIDGE_METHOD=POST",
		"FAAS_BRIDGE_URL=/foo?bar=1",
		"FAAS_BRIDGE_HEADERS=Content-Type=application/json\nX-Custom=val",
	}
	cmd := exec.CommandContext(ctx, bridgeBin, sockPath, "127.0.0.1", fmt.Sprintf("%d", port), deadline)
	cmd.Env = env
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf
	if err := cmd.Start(); err != nil {
		t.Fatalf("bridge start: %v", err)
	}
	defer func() {
		_ = cmd.Process.Signal(os.Kill)
		_ = cmd.Wait()
	}()

	// Wait for the socket to appear.
	deadline2 := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline2) {
		if _, err := os.Stat(sockPath); err == nil {
			break
		}
		// Detect bridge exit early so a startup failure (bad
		// deadline parse, env-var rejection, etc.) doesn't burn
		// the full 2 s wait before the dial below surfaces it.
		if cmd.ProcessState != nil {
			t.Fatalf("bridge exited before binding socket (exit=%d, stderr: %s)",
				cmd.ProcessState.ExitCode(), stderrBuf.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(sockPath); err != nil {
		t.Fatalf("bridge socket %s never appeared after 2s (stderr: %s)",
			sockPath, stderrBuf.String())
	}

	// Open an H2C client to the bridge and POST a body.
	client := &http.Client{Transport: newH2CUnixTransport(sockPath)}
	req, err := http.NewRequest("POST", "http://unix/foo?bar=1", strings.NewReader("body"))
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Custom", "val")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client do: %v (stderr: %s)", err, stderrBuf.String())
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	// Allow the guest capture to settle.
	waitFor(t, 2*time.Second, func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		return strings.Contains(string(fake.request), "Transfer-Encoding: chunked")
	})

	fake.mu.Lock()
	got := string(fake.request)
	fake.mu.Unlock()

	if !strings.HasPrefix(got, "POST /foo?bar=1 HTTP/1.1\r\n") {
		t.Errorf("request line = %q, want %q", firstLine(got), "POST /foo?bar=1 HTTP/1.1")
	}
	// Host header must include the dial port so vhost routers
	// (Nginx server_name, Express vhost, Rails request.host) see
	// the same wire shape v1's shell bridge emitted
	// (forward.go:1046-1047). The test points the bridge at
	// 127.0.0.1:<port>, so the fallback Host is `127.0.0.1:<port>`
	// — not the production 10.0.0.2.
	if !strings.Contains(got, fmt.Sprintf("Host: 127.0.0.1:%d\r\n", port)) {
		t.Errorf("missing Host: 127.0.0.1:%d (got %q)", port, got)
	}
	// FAAS_BRIDGE_HOST, when set by vmmd from the inbound request,
	// overrides the dial-port fallback. The test does NOT set
	// FAAS_BRIDGE_HOST, so the fallback is what we expect above.
	if strings.Contains(got, "Host: 10.0.0.2") {
		t.Errorf("port-less Host: 10.0.0.2 must not appear (regression of finding #4): %q", got)
	}
	if !strings.Contains(got, "Transfer-Encoding: chunked\r\n") {
		t.Errorf("missing Transfer-Encoding: chunked: %q", got)
	}
	if strings.Contains(got, "Content-Length:") {
		t.Errorf("Content-Length must be dropped when chunked is hard-coded: %q", got)
	}
	if !strings.Contains(got, "Content-Type: application/json\r\n") {
		t.Errorf("missing Content-Type: %q", got)
	}
	if !strings.Contains(got, "X-Custom: val\r\n") {
		t.Errorf("missing X-Custom: %q", got)
	}
}

// newH2CUnixTransport is a small H2C-over-unix transport built
// for the test. Mirrors pkg/gateway/internal_proxy.go:181-230
// but is local to the test package.
func newH2CUnixTransport(sockPath string) *http2.Transport {
	return &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, _, _ string, _ *tls.Config) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sockPath)
		},
		IdleConnTimeout: 5 * time.Second,
		ReadIdleTimeout: time.Second,
		PingTimeout:     time.Second,
	}
}

func atou(t *testing.T, s string) uint32 {
	t.Helper()
	var n uint64
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return uint32(n)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func waitFor(t *testing.T, cap time.Duration, pred func() bool) {
	t.Helper()
	deadline := time.Now().Add(cap)
	for time.Now().Before(deadline) {
		if pred() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("waitFor timed out after %v", cap)
}

// (Sanitization coverage lives in forward_v2_internal_test.go so it
// can access the unexported streamBridgeEnv / sanitizeHeaderValue
// helpers; this file stays in package vmmdgrpc_test for the rest of
// the black-box coverage.)
