package fcvm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Compile-time proof the production VMM satisfies the interface the Manager uses.
var _ VMM = (*JailerVMM)(nil)

func TestProvisionRewritesPathsIntoChroot(t *testing.T) {
	// provision hardlinks images into the chroot and rewrites config paths to
	// their in-chroot basenames — the jailed firecracker sees only these.
	root := t.TempDir()
	srcDir := t.TempDir()
	kernel := filepath.Join(srcDir, "vmlinux-6.1")
	base := filepath.Join(srcDir, "runner-node22.ext4")
	layer := filepath.Join(srcDir, "layer-1.ext4")
	for _, f := range []string{kernel, base, layer} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := BuildColdBootConfig(ColdBootSpec{
		KernelPath: kernel, BasePath: base, LayerPath: layer,
		VcpuCount: 2, MemSizeMiB: 128, Tap: "tap0",
	}, 0)

	v := NewJailerVMM(t.TempDir(), 0)
	out, err := v.provision(root, cfg, 20000, 20000)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if out.BootSource.KernelImagePath != "vmlinux-6.1" {
		t.Errorf("kernel path = %q, want in-chroot basename", out.BootSource.KernelImagePath)
	}
	if out.Drives[0].PathOnHost != "runner-node22.ext4" || out.Drives[1].PathOnHost != "layer-1.ext4" {
		t.Errorf("drive paths not rewritten: %q, %q", out.Drives[0].PathOnHost, out.Drives[1].PathOnHost)
	}
	// Files must actually exist in the chroot root now.
	for _, name := range []string{"vmlinux-6.1", "runner-node22.ext4", "layer-1.ext4"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Errorf("expected %s provisioned into chroot: %v", name, err)
		}
	}
	// The original config is untouched (we returned a copy).
	if cfg.BootSource.KernelImagePath != kernel {
		t.Error("provision mutated the input config")
	}
}

func TestStageReadOnly_HardlinksAndWidensRead(t *testing.T) {
	// A 0600 source must end up readable by other (o+r) after staging, and share
	// the source inode (hardlink) — we never copy or chown a shared read-only file.
	dir := t.TempDir()
	src := filepath.Join(dir, "base.ext4")
	if err := os.WriteFile(src, []byte("base"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	name, err := stageReadOnly(root, src)
	if err != nil {
		t.Fatalf("stageReadOnly: %v", err)
	}
	dst := filepath.Join(root, name)
	a, _ := os.Stat(src)
	b, _ := os.Stat(dst)
	if !os.SameFile(a, b) {
		t.Error("stageReadOnly should hardlink the shared source, not copy it")
	}
	if b.Mode().Perm()&0o004 == 0 {
		t.Errorf("staged read-only file mode %v is not other-readable", b.Mode().Perm())
	}
}

func TestStageWritable_CopiesPrivateAndUnlinksFromSource(t *testing.T) {
	// The writable drive must be an independent copy (never the shared inode) so a
	// guest write can't corrupt the source under a concurrent instance.
	dir := t.TempDir()
	src := filepath.Join(dir, "layer.ext4")
	if err := os.WriteFile(src, []byte("layer"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	name, err := stageWritable(root, src, 20000, 20000)
	if err != nil {
		t.Fatalf("stageWritable: %v", err)
	}
	dst := filepath.Join(root, name)
	a, _ := os.Stat(src)
	b, _ := os.Stat(dst)
	if os.SameFile(a, b) {
		t.Error("stageWritable must copy — the writable drive must not alias the source inode")
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "layer" {
		t.Fatalf("copied content = %q err=%v", got, err)
	}
	if b.Mode().Perm() != 0o600 {
		t.Errorf("writable drive mode = %v, want 0600", b.Mode().Perm())
	}
}

func TestStageWritable_ReplacesHardlinkedSibling(t *testing.T) {
	// M0 points drive0 and drive1 at the same image: the read-only drive hardlinks
	// it in first, then the writable drive must break that link and copy — not
	// truncate the source through the shared inode.
	dir := t.TempDir()
	src := filepath.Join(dir, "busybox.ext4")
	if err := os.WriteFile(src, []byte("busybox-image"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := stageReadOnly(root, src); err != nil { // drive0 hardlinks it in
		t.Fatalf("stageReadOnly: %v", err)
	}
	if _, err := stageWritable(root, src, 20000, 20000); err != nil { // drive1 copies
		t.Fatalf("stageWritable: %v", err)
	}
	// The source must be intact (not truncated) and the staged copy independent.
	got, err := os.ReadFile(src)
	if err != nil || string(got) != "busybox-image" {
		t.Fatalf("source corrupted after writable staging: got %q err=%v", got, err)
	}
	a, _ := os.Stat(src)
	b, _ := os.Stat(filepath.Join(root, "busybox.ext4"))
	if os.SameFile(a, b) {
		t.Error("writable staging left the chroot file aliased to the source inode")
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "sub", "dst")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "hello" {
		t.Fatalf("copied content = %q, err=%v", got, err)
	}
}

func TestLinkInto_Hardlink(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "mem.bin")
	if err := os.WriteFile(src, []byte("contents"), 0o644); err != nil {
		t.Fatal(err)
	}
	dstDir := filepath.Join(dir, "dest")
	if err := os.MkdirAll(dstDir, 0o750); err != nil {
		t.Fatal(err)
	}
	name, err := linkInto(dstDir, src)
	if err != nil {
		t.Fatalf("linkInto: %v", err)
	}
	if name != "mem.bin" {
		t.Errorf("returned name = %q, want mem.bin", name)
	}
	a, _ := os.Stat(src)
	b, _ := os.Stat(filepath.Join(dstDir, "mem.bin"))
	if !os.SameFile(a, b) {
		t.Error("expected hardlink (same inode)")
	}
}

func TestLinkInto_OverwritesExisting(t *testing.T) {
	// If dst already exists, linkInto must remove it before hardlinking.
	dir := t.TempDir()
	src := filepath.Join(dir, "f")
	if err := os.WriteFile(src, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	dstDir := filepath.Join(dir, "dst")
	if err := os.MkdirAll(dstDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, "f"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	name, err := linkInto(dstDir, src)
	if err != nil {
		t.Fatalf("linkInto: %v", err)
	}
	if name != "f" {
		t.Errorf("name = %q", name)
	}
	got, err := os.ReadFile(filepath.Join(dstDir, "f"))
	if err != nil || string(got) != "new" {
		t.Errorf("after overwrite: got %q err=%v", got, err)
	}
}

func TestMoveOut_Rename(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "snap")
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "sub", "snap")
	size, err := moveOut(src, dst)
	if err != nil {
		t.Fatalf("moveOut: %v", err)
	}
	if size != int64(len("payload")) {
		t.Errorf("size = %d, want %d", size, len("payload"))
	}
	if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("src should be gone after moveOut, stat err=%v", err)
	}
}

func TestMoveOut_CrossDeviceFallback(t *testing.T) {
	// /tmp and the temp dir are usually the same fs, but we can simulate the
	// fallback by removing the parent of dst so MkdirAll has to create it
	// (rename should still work; this is the happy rename branch — the
	// cross-device fallback is exercised by integration tests).
	dir := t.TempDir()
	src := filepath.Join(dir, "snap")
	if err := os.WriteFile(src, []byte("xx"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "new", "snap")
	size, err := moveOut(src, dst)
	if err != nil || size != 2 {
		t.Fatalf("moveOut happy: size=%d err=%v", size, err)
	}
}

func TestChrootRoot_AndSocketPath(t *testing.T) {
	v := NewJailerVMM("/srv/fc/jail", 30*time.Second)
	got := v.chrootRoot("inst-1")
	if !strings.HasPrefix(got, "/srv/fc/jail") {
		t.Errorf("chrootRoot = %q, want under /srv/fc/jail", got)
	}
	if !strings.HasSuffix(got, "/root") {
		t.Errorf("chrootRoot = %q, want suffix /root", got)
	}
	sock := v.socketPath("inst-1")
	if !strings.HasSuffix(sock, APISockName) {
		t.Errorf("socketPath = %q, want suffix %q", sock, APISockName)
	}
	if !strings.Contains(sock, "inst-1") {
		t.Errorf("socketPath = %q, want contains inst-1", sock)
	}
}

func TestDetectFirecrackerVersion_MissingBinary(t *testing.T) {
	// Set PATH to an empty dir so the real firecracker binary is invisible.
	t.Setenv("PATH", t.TempDir())
	_, err := DetectFirecrackerVersion(context.Background())
	if err == nil {
		t.Fatal("DetectFirecrackerVersion should fail when binary missing")
	}
}

// stubFC is a tiny script that pretends to be firecracker and prints a
// fixed version line. Used when the real binary is unavailable on CI.
func TestDetectFirecrackerVersion_WithStub(t *testing.T) {
	if _, err := exec.LookPath("firecracker"); err == nil {
		t.Skip("real firecracker present; stub test not needed")
	}
	binDir := t.TempDir()
	stub := filepath.Join(binDir, "firecracker")
	script := "#!/bin/sh\necho 'Firecracker v9.9.9-rc1'\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	v, err := DetectFirecrackerVersion(context.Background())
	if err != nil {
		t.Fatalf("DetectFirecrackerVersion: %v", err)
	}
	if v != "9.9.9-rc1" {
		t.Errorf("version = %q, want 9.9.9-rc1", v)
	}
}

// --- apiCall / apiPut / apiPatch / fcClient -------------------------------
//
// We exercise the production wire format (HTTP over a unix socket) by binding
// an httptest server to the same kind of socket the real jailer creates. The
// VMM's fcClient() honors its `unix://...` socket path; we pretend the
// "firecracker" server is listening there. This is the cheapest way to cover
// the request-marshal, status-code, body-error, and connection-failure
// branches of apiCall without KVM.

// bindTestSocket spins up an httptest.Server whose listener is re-bound to a
// real unix socket at `sockPath`. Returns the server and a cleanup. We do
// this so the VMM's http.Transport (which dials `unix sockPath`) succeeds.
//
// macOS sun_path is 104 bytes; we keep the sock path short via a /tmp symlink
// to the real t.TempDir (mirrors the unixsock_test.go pattern).
func bindTestSocket(t *testing.T, sockPath string, handler http.Handler) *httptest.Server {
	t.Helper()
	// httptest.NewUnstartedServer + Listener swapping.
	ts := httptest.NewUnstartedServer(handler)

	// Build a unix listener on the desired path.
	_ = os.Remove(sockPath)
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o750); err != nil {
		t.Fatalf("mkdir sock dir: %v", err)
	}
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix %s: %v", sockPath, err)
	}
	ts.Listener.Close() // discard the TCP listener
	ts.Listener = l
	ts.Start()
	t.Cleanup(ts.Close)
	return ts
}

// shortBase returns a chrootBase whose absolute path is short enough for
// sockaddr_un.sun_path on macOS (104 byte limit). We symlink
// /tmp/fcvm-<test>-<idx> to t.TempDir() so the JailerVMM's chrootRoot + sock
// path fits inside sun_path.
func shortBase(t *testing.T) string {
	t.Helper()
	real := t.TempDir()
	short := fmt.Sprintf("/tmp/fcvms-%s", t.Name())
	// Sanitize — t.Name() may contain '/' in subtests.
	short = strings.ReplaceAll(short, "/", "-")
	if err := os.Symlink(real, short); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(short) })
	return short
}

// TestAPICall_Success drives apiCall against a server that returns 204 No
// Content — the canonical "happy" branch for /vm PATCH and /snapshot/load
// PUT. Verifies the JSON body is well-formed and the path lands on the
// server side.
func TestAPICall_Success(t *testing.T) {
	base := shortBase(t)
	inst := "is"
	sock := filepath.Join(base, "firecracker", inst, "root", APISockName)

	var gotPath, gotMethod, gotCT string
	var gotBody map[string]any
	srv := bindTestSocket(t, sock, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotCT = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))

	v := NewJailerVMM(base, time.Second)
	if err := v.apiPut(context.Background(), inst, "/vm/instance-action", map[string]any{"action_type": "SendCtrlAltDel"}); err != nil {
		t.Fatalf("apiPut: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %q, want PUT", gotMethod)
	}
	if gotPath != "/vm/instance-action" {
		t.Errorf("path = %q", gotPath)
	}
	if gotCT != "application/json" {
		t.Errorf("content-type = %q", gotCT)
	}
	if gotBody["action_type"] != "SendCtrlAltDel" {
		t.Errorf("body = %v", gotBody)
	}

	// apiPatch should map to PATCH; verify the same path & verb split.
	if err := v.apiPatch(context.Background(), inst, "/vm", map[string]any{"state": "Paused"}); err != nil {
		t.Fatalf("apiPatch: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("apiPatch method = %q, want PATCH", gotMethod)
	}
	if gotPath != "/vm" {
		t.Errorf("apiPatch path = %q", gotPath)
	}
	if gotBody["state"] != "Paused" {
		t.Errorf("apiPatch body = %v", gotBody)
	}
	// Sanity: the server was actually used.
	_ = srv
}

// TestAPICall_Non2xxReturnsFormattedError covers the branch that reads up to
// 4 KiB of the error body and formats method + path + status + body. This is
// the path /vm PATCH returns when, e.g., the VM isn't running.
func TestAPICall_Non2xxReturnsFormattedError(t *testing.T) {
	base := shortBase(t)
	inst := "ie"
	sock := filepath.Join(base, "firecracker", inst, "root", APISockName)
	bindTestSocket(t, sock, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"instance-action invalid in current state"}`)
	}))

	v := NewJailerVMM(base, time.Second)
	err := v.apiPatch(context.Background(), inst, "/vm", nil)
	if err == nil {
		t.Fatal("expected error from non-2xx response")
	}
	for _, want := range []string{"PATCH", "/vm", "400 Bad Request", "instance-action invalid"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

// TestAPICall_BadJSON covers json.Marshal failing on a non-marshalable body
// (channels marshal-error). apiCall must short-circuit before hitting the
// network.
func TestAPICall_BadJSON(t *testing.T) {
	v := NewJailerVMM(t.TempDir(), time.Second)
	err := v.apiPut(context.Background(), "any", "/x", make(chan int))
	if err == nil {
		t.Fatal("expected json marshal error")
	}
	if !strings.Contains(err.Error(), "json") {
		t.Errorf("error %q does not look like a marshal failure", err.Error())
	}
}

// TestAPICall_ConnectionFailure covers the path where the socket doesn't
// exist (no server bound). The dial error must propagate, not be swallowed.
func TestAPICall_ConnectionFailure(t *testing.T) {
	base := t.TempDir()
	// Don't create the socket — every dial fails with ENOENT/ENOENT-like.
	v := NewJailerVMM(base, time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err := v.apiPut(ctx, "nope", "/vm", nil)
	if err == nil {
		t.Fatal("expected dial error when socket missing")
	}
}

// TestAPICall_ContextCancellation covers ctx cancellation: the request must
// error out without hanging the test.
func TestAPICall_ContextCancellation(t *testing.T) {
	base := shortBase(t)
	inst := "ic"
	sock := filepath.Join(base, "firecracker", inst, "root", APISockName)
	bindTestSocket(t, sock, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until the client cancels, then return 200 — we just want to
		// verify apiCall honors context.
		<-r.Context().Done()
		w.WriteHeader(http.StatusOK)
	}))

	v := NewJailerVMM(base, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := v.apiPut(ctx, inst, "/x", nil)
	if err == nil {
		t.Fatal("expected ctx cancellation to surface")
	}
}

// TestFcClient_Caches verifies two fcClient calls for the same instance
// return the same pointer (so the http.Transport connection pool is reused).
func TestFcClient_Caches(t *testing.T) {
	v := NewJailerVMM(t.TempDir(), time.Second)
	a := v.fcClient("i1")
	b := v.fcClient("i1")
	if a != b {
		t.Errorf("fcClient not cached: %p vs %p", a, b)
	}
	c := v.fcClient("i2")
	if a == c {
		t.Errorf("fcClient leaked clients across instances: %p == %p", a, c)
	}
}

// TestCloseClient_DropsCached verifies Kill's seam actually evicts the cached
// client (so the next Boot of the same instance name gets a fresh client).
func TestCloseClient_DropsCached(t *testing.T) {
	v := NewJailerVMM(t.TempDir(), time.Second)
	a := v.fcClient("killme")
	v.closeClient("killme")
	b := v.fcClient("killme")
	if a == b {
		t.Errorf("closeClient did not evict: %p", a)
	}
	// Closing an instance that wasn't cached is a no-op.
	v.closeClient("never-existed")
}

// TestAPICall_ResponseBodyCloseIsBestEffort covers the success-with-body path:
// the server returns 200 + a payload (Firecracker does for /machine-config
// GET, but apiCall ignores the body). We assert no panic and no leaked FD by
// hammering the call a few times against a counting handler.
func TestAPICall_ResponseBodyCloseIsBestEffort(t *testing.T) {
	base := shortBase(t)
	inst := "ib"
	sock := filepath.Join(base, "firecracker", inst, "root", APISockName)
	var hits atomic.Int64
	bindTestSocket(t, sock, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"some":"payload"}`)
	}))

	v := NewJailerVMM(base, time.Second)
	for i := 0; i < 25; i++ {
		if err := v.apiPatch(context.Background(), inst, "/x", nil); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if got := hits.Load(); got != 25 {
		t.Errorf("hits = %d, want 25", got)
	}
}

// TestAPICall_ErrorBodyTruncatedAt4KiB documents the "read at most 4096
// bytes of error body" contract — that's the cap so a chatty Firecracker
// can't blow up our log lines.
func TestAPICall_ErrorBodyTruncatedAt4KiB(t *testing.T) {
	base := shortBase(t)
	inst := "it"
	sock := filepath.Join(base, "firecracker", inst, "root", APISockName)
	huge := strings.Repeat("X", 8192)
	bindTestSocket(t, sock, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, huge)
	}))

	v := NewJailerVMM(base, time.Second)
	err := v.apiPut(context.Background(), inst, "/x", nil)
	if err == nil {
		t.Fatal("expected 500 error")
	}
	// The reported body must be ≤ 4 KiB plus the small prefix.
	if len(err.Error()) > 4200 {
		t.Errorf("error message %d bytes — body cap 4 KiB not honored", len(err.Error()))
	}
}

// --- Kill / mkChroot / waitReady (no-KVM) ---------------------------------

// TestKill_IdempotentWithoutProcess covers the no-jailer-running case: Kill
// must succeed and remove the chroot dir (creating it if absent is fine, but
// our impl expects it to exist OR to be safely absent).
func TestKill_IdempotentWithoutProcess(t *testing.T) {
	base := t.TempDir()
	inst := "kill-idemp"
	// Plant the chroot so we can verify RemoveAll took effect.
	root := filepath.Join(base, FirecrackerBin, inst)
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "marker"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}

	v := NewJailerVMM(base, time.Second)
	if err := v.Kill(context.Background(), Lease{Instance: inst, UID: 20000, GID: 20000}); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("chroot should be removed, stat err=%v", err)
	}
	// Second Kill is a no-op (no error).
	if err := v.Kill(context.Background(), Lease{Instance: inst}); err != nil {
		t.Errorf("second Kill: %v", err)
	}
}

// TestKill_RemovesCachedClient proves the cache eviction actually happens on
// the production path — see apiCall tests for the unit-level proof.
func TestKill_RemovesCachedClient(t *testing.T) {
	base := t.TempDir()
	v := NewJailerVMM(base, time.Second)
	_ = v.fcClient("kill-cache")
	v.Kill(context.Background(), Lease{Instance: "kill-cache"})
	v.mu.Lock()
	_, stillCached := v.clients["kill-cache"]
	v.mu.Unlock()
	if stillCached {
		t.Error("Kill did not evict cached http.Client")
	}
}

// TestMkChroot_CreatesDirectory exercises the helper directly — it's the
// foundation of Boot/Restore and we want it covered even if Boot itself
// isn't.
func TestMkChroot_CreatesDirectory(t *testing.T) {
	base := t.TempDir()
	v := NewJailerVMM(base, time.Second)
	root, err := v.mkChroot("new")
	if err != nil {
		t.Fatalf("mkChroot: %v", err)
	}
	if !strings.HasSuffix(root, filepath.Join("new", "root")) {
		t.Errorf("root = %q, want suffix new/root", root)
	}
	fi, err := os.Stat(root)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !fi.IsDir() {
		t.Error("mkChroot result is not a directory")
	}
	// Second call is idempotent.
	if _, err := v.mkChroot("new"); err != nil {
		t.Errorf("mkChroot idempotent: %v", err)
	}
}

// TestMkChroot_BadBaseReturnsError covers mkChroot failing on a path under a
// file (not a dir). mkChroot first RemoveAll's any stale state (so jailer
// gets a fresh /dev/net/tun) then MkdirAll's the path; either step can be
// the one that fails here. Pin both wrappers explicitly — relaxing to just
// "chroot" would silently accept a future refactor that introduces an
// unrelated chroot-step before RemoveAll.
func TestMkChroot_BadBaseReturnsError(t *testing.T) {
	base := t.TempDir()
	// Plant a file at the path MkdirAll would need to be a directory.
	conflict := filepath.Join(base, FirecrackerBin)
	if err := os.WriteFile(conflict, []byte("not-a-dir"), 0o640); err != nil {
		t.Fatal(err)
	}
	v := NewJailerVMM(base, time.Second)
	_, err := v.mkChroot("anything")
	if err == nil {
		t.Fatal("expected mkChroot error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "wipe stale chroot") && !strings.Contains(msg, "mkdir chroot") {
		t.Errorf("error %q must wrap either 'wipe stale chroot' or 'mkdir chroot' (got %q)", msg, msg)
	}
}

// TestWaitReady_SucceedsOnListener verifies the readiness poller returns nil
// as soon as a TCP listener is reachable.
func TestWaitReady_SucceedsOnListener(t *testing.T) {
	// Pick a free port and listen; we never accept — DialTimeout succeeds
	// once the kernel hands us a SYN-ACK-shaped completion.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	host, port, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var p int
	if _, err := fmt.Sscanf(port, "%d", &p); err != nil {
		t.Fatal(err)
	}

	v := NewJailerVMM(t.TempDir(), 2*time.Second)
	lease := Lease{Instance: "ready", HostIP: netip.MustParseAddr(host), UID: 1, GID: 1}
	// waitReady dials "host:8080" — patch by overriding the port via a
	// re-implementation bound to the actual port. Easier: listen on :8080 if
	// free, else skip.
	_ = p
	if ln, err := net.Listen("tcp", "127.0.0.1:8080"); err == nil {
		defer ln.Close()
		lease = Lease{Instance: "ready", HostIP: netip.MustParseAddr("127.0.0.1"), UID: 1, GID: 1}
	}
	if err := v.waitReady(context.Background(), lease); err != nil {
		t.Errorf("waitReady: %v", err)
	}
}

// TestWaitReady_TimesOut verifies the readiness budget fires when the
// listener never accepts (port 1 is reserved and refuses on Linux/macOS).
func TestWaitReady_TimesOut(t *testing.T) {
	v := NewJailerVMM(t.TempDir(), 150*time.Millisecond)
	// 192.0.2.1 is TEST-NET-1 (RFC 5737) — guaranteed unrouted, so Dial
	// fails fast and the loop must time out at readyTimeout.
	lease := Lease{Instance: "slow", HostIP: netip.MustParseAddr("192.0.2.1")}
	start := time.Now()
	err := v.waitReady(context.Background(), lease)
	if err == nil {
		t.Fatal("expected timeout")
	}
	if !strings.Contains(err.Error(), "not ready after") {
		t.Errorf("error %q missing 'not ready after'", err.Error())
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Errorf("returned too fast (%s); budget should have been honored", elapsed)
	}
}

// TestWaitReady_ContextCanceled ensures cancellation surfaces rather than
// waiting out the full budget.
func TestWaitReady_ContextCanceled(t *testing.T) {
	v := NewJailerVMM(t.TempDir(), 5*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	lease := Lease{Instance: "x", HostIP: netip.MustParseAddr("192.0.2.1")}
	if err := v.waitReady(ctx, lease); err == nil {
		t.Fatal("expected ctx error")
	}
}

// --- Boot / Restore / Snapshot (smoke) -----------------------------------

// TestBoot_MkChrootFailure covers Boot's first failure branch: mkChroot
// errors → Boot returns the wrapped error and the deferred Kill doesn't run
// (because the cmd was never started).
func TestBoot_MkChrootFailure(t *testing.T) {
	base := t.TempDir()
	conflict := filepath.Join(base, FirecrackerBin)
	if err := os.WriteFile(conflict, []byte("file"), 0o640); err != nil {
		t.Fatal(err)
	}
	v := NewJailerVMM(base, time.Second)
	err := v.Boot(context.Background(), Lease{Instance: "boot-fail", UID: 20000, GID: 20000}, VMConfig{})
	if err == nil {
		t.Fatal("expected mkChroot failure")
	}
	// mkChroot now RemoveAll's the path first (to scrub stale jailer state
	// from a prior failed run) then MkdirAll's it. Either step can fail
	// on a non-empty parent. Pin both wrappers so the test catches a future
	// refactor that introduces an unrelated chroot-step before RemoveAll.
	msg := err.Error()
	if !strings.Contains(msg, "wipe stale chroot") && !strings.Contains(msg, "mkdir chroot") {
		t.Errorf("error %q must wrap either 'wipe stale chroot' or 'mkdir chroot'", msg)
	}
}

// TestRestore_MkChrootFailure mirrors Boot — same seam, different code path.
func TestRestore_MkChrootFailure(t *testing.T) {
	base := t.TempDir()
	conflict := filepath.Join(base, FirecrackerBin)
	if err := os.WriteFile(conflict, []byte("file"), 0o640); err != nil {
		t.Fatal(err)
	}
	v := NewJailerVMM(base, time.Second)
	err := v.Restore(context.Background(), Lease{Instance: "restore-fail"}, RestoreSpec{
		MemPath: "/nonexistent/mem", VMStatePath: "/nonexistent/vmstate",
		KernelPath: "/nonexistent/kernel", BasePath: "/nonexistent/base", LayerPath: "/nonexistent/layer",
	})
	if err == nil {
		t.Fatal("expected mkChroot failure")
	}
}

// TestKill_ChrootRemoveErrorFailsWhenBaseIsFile covers the only path that
// returns an error from Kill: RemoveAll fails because the base path isn't
// a directory.
func TestKill_ChrootRemoveErrorFailsWhenBaseIsFile(t *testing.T) {
	// Need chrootBase to be a regular file so RemoveAll(<base>/firecracker/x)
	// returns ENOTDIR. Put the file inside a temp dir.
	dir := t.TempDir()
	base := filepath.Join(dir, "iamafile")
	if err := os.WriteFile(base, []byte("not-a-dir"), 0o640); err != nil {
		t.Fatal(err)
	}
	v := NewJailerVMM(base, time.Second)
	err := v.Kill(context.Background(), Lease{Instance: "x"})
	if err == nil {
		t.Fatal("expected RemoveAll to fail")
	}
	if !strings.Contains(err.Error(), "remove chroot") {
		t.Errorf("error %q missing 'remove chroot'", err.Error())
	}
}

// ---- ADR-022 vsock + post-restore resume hook -----------------------

// TestGuestVsockCIDSkipsReserved pins the slot→CID derivation: every slot
// must produce a guest_cid that does NOT collide with the reserved kernel
// vsock addresses (0/1/2) and is unique per slot.
func TestGuestVsockCIDSkipsReserved(t *testing.T) {
	seen := map[uint32]bool{}
	for slot := 0; slot < 16; slot++ {
		cid := GuestVsockCID(slot)
		if cid < VsockCIDBase {
			t.Errorf("slot=%d -> cid=%d, must be >= VsockCIDBase=%d to skip reserved range", slot, cid, VsockCIDBase)
		}
		if seen[cid] {
			t.Errorf("slot=%d -> cid=%d collides with another slot", slot, cid)
		}
		seen[cid] = true
	}
	if got := GuestVsockCID(0); got != VsockCIDBase {
		t.Errorf("GuestVsockCID(0) = %d, want VsockCIDBase=%d", got, VsockCIDBase)
	}
	if got := GuestVsockCID(7); got != VsockCIDBase+7 {
		t.Errorf("GuestVsockCID(7) = %d, want %d", got, VsockCIDBase+7)
	}
}

// TestBuildColdBootConfigIncludesVsockDevice asserts the cold-boot config
// always attaches a vsock device whose guest_cid is slot-derived and whose
// UDS is the chroot-relative VsockUDSSocketName.
func TestBuildColdBootConfigIncludesVsockDevice(t *testing.T) {
	cfg := BuildColdBootConfig(validColdSpec(), 7)
	if cfg.VsockDevice == nil {
		t.Fatal("VsockDevice = nil, want attached (ADR-022)")
	}
	if cfg.VsockDevice.ID != VsockDeviceID {
		t.Errorf("VsockDevice.ID = %q, want %q", cfg.VsockDevice.ID, VsockDeviceID)
	}
	if cfg.VsockDevice.GuestCID != GuestVsockCID(7) {
		t.Errorf("VsockDevice.GuestCID = %d, want %d", cfg.VsockDevice.GuestCID, GuestVsockCID(7))
	}
	if cfg.VsockDevice.UDSSocket != VsockUDSSocketName {
		t.Errorf("VsockDevice.UDSSocket = %q, want %q", cfg.VsockDevice.UDSSocket, VsockUDSSocketName)
	}
}

// fakeVsockUDSServer pretends to be the Firecracker vsock UDS that
// TriggerResumeHook dials. It mirrors the FC host-initiated protocol:
// accept, expect "CONNECT <port>\n", reply "OK <hostside_port>\n",
// then read the resume-hook payload ([4 BE msg type][JSON body]) and
// write the 1-byte ack. The captured HostTimeUnixNano + entropy bytes flow
// through onHook so tests can assert wire formatting.
func fakeVsockUDSServer(t *testing.T, sockPath string, ack byte, onHook func(hostTimeUnixNano int64, entropy []byte) error) {
	t.Helper()
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen %s: %v", sockPath, err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		// Step 1: read "CONNECT <port>\n". Read byte-by-byte until newline
		// so we don't block on ReadFull waiting for a fixed count that the
		// port number's digit count might vary.
		connectBuf := make([]byte, 0, 32)
		readByte := make([]byte, 1)
		for {
			if _, err := c.Read(readByte); err != nil {
				_, _ = c.Write([]byte{1})
				return
			}
			connectBuf = append(connectBuf, readByte[0])
			if readByte[0] == '\n' {
				break
			}
			if len(connectBuf) > 32 {
				_, _ = c.Write([]byte{1})
				return
			}
		}
		if !strings.HasPrefix(string(connectBuf), "CONNECT ") {
			_, _ = c.Write([]byte{1})
			return
		}
		// Step 2: reply "OK <hostside_port>\n". FC picks an ephemeral
		// port; the value is for multiplexing bookkeeping and the
		// host-side reader doesn't validate it.
		if _, err := c.Write([]byte("OK 1073741824\n")); err != nil {
			return
		}

		// Step 3: read the resume-hook payload. Format: 4-byte BE msg type +
		// 4-byte BE body length + JSON body. Read exactly 8 bytes header,
		// then the body length bytes.
		var hdr [8]byte
		if _, err := io.ReadFull(c, hdr[:]); err != nil {
			_, _ = c.Write([]byte{1})
			return
		}
		bodyLen := binary.BigEndian.Uint32(hdr[4:8])
		body := make([]byte, bodyLen)
		if _, err := io.ReadFull(c, body); err != nil {
			_, _ = c.Write([]byte{1})
			return
		}
		var payload struct {
			Nano     int64  `json:"hostTimeUnixNano"`
			EntropyB string `json:"entropy"`
		}
		_ = json.Unmarshal(body, &payload)
		var entropy []byte
		if payload.EntropyB != "" {
			entropy, _ = base64.StdEncoding.DecodeString(payload.EntropyB)
		}
		if onHook != nil {
			if err := onHook(payload.Nano, entropy); err != nil {
				_, _ = c.Write([]byte{1})
				return
			}
		}
		_, _ = c.Write([]byte{ack})
	}()
}

// shortChrootBase is the chroot root used by all unit tests that touch
// vsock / api unix sockets. macOS' sun_path is 104 bytes; t.TempDir()'s path
// plus "firecracker/<inst>/root/<sock>" already pushes past that on long
// test names. Using the TMPDIR-rooted short path keeps us under the limit on
// both Linux and darwin without losing isolation (each test calls
// os.MkdirAll + os.RemoveAll under its own subdir).
func shortChrootBase(t *testing.T, sub string) string {
	t.Helper()
	// /tmp/<pid>-<sub> keeps the path short and pid-unique for parallel tests.
	p := filepath.Join(os.TempDir(), fmt.Sprintf("fcvmt-%d-%s", os.Getpid(), sub))
	if err := os.RemoveAll(p); err != nil {
		t.Fatalf("clean %s: %v", p, err)
	}
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(p) })
	return p
}

// TestTriggerResumeHookDialSuccess: when the UDS is listening and acks ok,
// TriggerResumeHook returns nil and the wire format is correct.
func TestTriggerResumeHookDialSuccess(t *testing.T) {
	chrootBase := shortChrootBase(t, "dialok")
	instance := "iA"
	// Path must mirror v.chrootRoot(iA) = <base>/<fcName>/<inst>/root.
	// macOS' sun_path is 104 bytes; chrootBase + /f/iA/root/vsock.sock
	// stays under it because chrootBase is TMPDIR-rooted and shortChrootBase
	// is pid-unique.
	root := filepath.Join(chrootBase, "f", instance, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	sockPath := filepath.Join(root, VsockUDSSocketName)

	var seenNano int64
	var seenEntropy []byte
	var firstEntropy []byte
	var callCount int32
	// fakeVsockUDSServer's accept loop is single-shot. For the back-to-back
	// dials below (assertion: every dial sends fresh entropy), spin up a
	// persistent loop instead. We do that inline so the test doesn't need
	// a new helper.
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen %s: %v", sockPath, err)
	}
	t.Cleanup(func() { _ = l.Close() })
	// mu guards seenNano / seenEntropy / firstEntropy against concurrent
	// writes from the per-connection goroutines. Without it, go test -race
	// flags the back-to-back test (a previous version of this test relied
	// on the callCount atomic for ordering but read byte slices with no
	// happens-before edge).
	var mu sync.Mutex
	go func() {
		for i := 0; i < 2; i++ {
			c, aErr := l.Accept()
			if aErr != nil {
				return
			}
			idx := i
			go handleFakeVsockHook(t, c, ackOK, func(nano int64, entropy []byte) error {
				atomic.AddInt32(&callCount, 1)
				mu.Lock()
				seenNano = nano
				seenEntropy = entropy
				if idx == 0 {
					firstEntropy = append([]byte(nil), entropy...)
				}
				mu.Unlock()
				return nil
			})
		}
	}()

	v := &JailerVMM{chrootBase: chrootBase, fcName: "f"}
	lease := Lease{Instance: instance, Slot: 7}
	const wantNano = int64(1700000000123456789)
	if err := v.TriggerResumeHook(context.Background(), lease, wantNano); err != nil {
		t.Fatalf("TriggerResumeHook: %v", err)
	}
	if err := v.TriggerResumeHook(context.Background(), lease, wantNano); err != nil {
		t.Fatalf("TriggerResumeHook (second): %v", err)
	}
	// Wait for both goroutines to finish recording.
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&callCount) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if atomic.LoadInt32(&callCount) != 2 {
		t.Fatalf("hook invocations = %d, want 2", callCount)
	}
	mu.Lock()
	recordedNano := seenNano
	recordedEntropy := append([]byte(nil), seenEntropy...)
	recordedFirst := append([]byte(nil), firstEntropy...)
	mu.Unlock()
	if recordedNano != wantNano {
		t.Errorf("hook saw hostTimeUnixNano=%d, want %d", recordedNano, wantNano)
	}
	if len(recordedEntropy) != resumeHookEntropyBytes {
		t.Errorf("hook saw entropy of %d bytes, want %d (host must ship exactly resumeHookEntropyBytes)", len(recordedEntropy), resumeHookEntropyBytes)
	}
	// Spec §11 V6 guarantee: every dial sends FRESH entropy. Two back-to-back
	// triggers with the same lease MUST yield different bytes (crypto/rand is
	// the only source — no caching, no slot-derivation, no deterministic seed).
	if len(recordedFirst) != resumeHookEntropyBytes {
		t.Fatalf("first entropy = %d bytes, want %d", len(recordedFirst), resumeHookEntropyBytes)
	}
	if bytes.Equal(recordedFirst, recordedEntropy) {
		t.Errorf("two back-to-back dials sent identical entropy; V6 acceptance would fail (host must use crypto/rand, not a fixed seed)")
	}
}

// handleFakeVsockHook drives one connection through the CONNECT-port handshake
// + length-prefixed resume payload + ack roundtrip. Extracted so the
// back-to-back test can spin up a multi-shot listener without a new helper.
func handleFakeVsockHook(t *testing.T, c net.Conn, ack byte, onHook func(hostTimeUnixNano int64, entropy []byte) error) {
	t.Helper()
	defer func() { _ = c.Close() }()
	connectBuf := make([]byte, 0, 32)
	readByte := make([]byte, 1)
	for {
		if _, err := c.Read(readByte); err != nil {
			_, _ = c.Write([]byte{1})
			return
		}
		connectBuf = append(connectBuf, readByte[0])
		if readByte[0] == '\n' {
			break
		}
		if len(connectBuf) > 32 {
			_, _ = c.Write([]byte{1})
			return
		}
	}
	if !strings.HasPrefix(string(connectBuf), "CONNECT ") {
		_, _ = c.Write([]byte{1})
		return
	}
	if _, err := c.Write([]byte("OK 1073741824\n")); err != nil {
		return
	}
	var hdr [8]byte
	if _, err := io.ReadFull(c, hdr[:]); err != nil {
		_, _ = c.Write([]byte{1})
		return
	}
	bodyLen := binary.BigEndian.Uint32(hdr[4:8])
	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(c, body); err != nil {
		_, _ = c.Write([]byte{1})
		return
	}
	var payload struct {
		Nano     int64  `json:"hostTimeUnixNano"`
		EntropyB string `json:"entropy"`
	}
	_ = json.Unmarshal(body, &payload)
	var entropy []byte
	if payload.EntropyB != "" {
		entropy, _ = base64.StdEncoding.DecodeString(payload.EntropyB)
	}
	if onHook != nil {
		if err := onHook(payload.Nano, entropy); err != nil {
			_, _ = c.Write([]byte{1})
			return
		}
	}
	_, _ = c.Write([]byte{ack})
}

const ackOK = byte(0)

// TestResumeHookBodyCapCoversEntropy pins the relationship between
// resumeHookEntropyBytes and resumeHookMaxBodyBytes. The cap is the
// CodeQL go/allocation-size-overflow guard — a future bump to either
// constant must keep `base64.StdEncoding.EncodedLen(entropy) + JSON
// envelope overhead` strictly below the cap, or the make() in
// TriggerResumeHook will allocate a negative-length slice and panic.
// This test catches a future bump that breaks the invariant.
func TestResumeHookBodyCapCoversEntropy(t *testing.T) {
	const envelopeSlack = 128 // JSON keys + punctuation + future expansion
	maxEncoded := base64.StdEncoding.EncodedLen(resumeHookEntropyBytes)
	if maxEncoded+envelopeSlack >= resumeHookMaxBodyBytes {
		t.Errorf("entropy cap too close to body cap: entropy encodes to %d bytes + %d envelope slack >= %d (cap); bump resumeHookMaxBodyBytes first",
			maxEncoded, envelopeSlack, resumeHookMaxBodyBytes)
	}
	if resumeHookMaxBodyBytes > 1<<20 {
		t.Errorf("body cap %d looks too large (> 1 MiB); check the design", resumeHookMaxBodyBytes)
	}
}

// TestTriggerResumeHookDialTimeout: when no UDS is listening,
// TriggerResumeHook returns within resumeHookDialDeadline with a wrapped
// "dial vsock uds" error.
func TestTriggerResumeHookDialTimeout(t *testing.T) {
	chrootBase := shortChrootBase(t, "dialto")
	instance := "iA"
	root := filepath.Join(chrootBase, "f", instance, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// No fakeVsockUDSServer — the file just doesn't exist.

	v := &JailerVMM{chrootBase: chrootBase, fcName: "f"}
	lease := Lease{Instance: instance, Slot: 0}
	ctx, cancel := context.WithTimeout(context.Background(), resumeHookDialDeadline+time.Second)
	defer cancel()
	err := v.TriggerResumeHook(ctx, lease, 1)
	if err == nil {
		t.Fatal("expected dial-timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "dial vsock uds") {
		t.Errorf("error %q missing 'dial vsock uds'", err.Error())
	}
}

// TestTriggerResumeHookAckPropagatesError: when the listener acks non-zero,
// TriggerResumeHook returns "resume hook failed (ack=N)".
func TestTriggerResumeHookAckPropagatesError(t *testing.T) {
	chrootBase := shortChrootBase(t, "ackerr")
	instance := "iA"
	root := filepath.Join(chrootBase, "f", instance, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	sockPath := filepath.Join(root, VsockUDSSocketName)
	fakeVsockUDSServer(t, sockPath, 1, nil) // nack

	v := &JailerVMM{chrootBase: chrootBase, fcName: "f"}
	lease := Lease{Instance: instance, Slot: 0}
	err := v.TriggerResumeHook(context.Background(), lease, 1)
	if err == nil {
		t.Fatal("expected nack error, got nil")
	}
	if !strings.Contains(err.Error(), "ack=1") {
		t.Errorf("error %q missing 'ack=1'", err.Error())
	}
}

// TestTriggerResumeHookContextCancel: a cancelled ctx surfaces immediately
// instead of burning the dial budget.
func TestTriggerResumeHookContextCancel(t *testing.T) {
	chrootBase := shortChrootBase(t, "ctxcan")
	instance := "iA"
	root := filepath.Join(chrootBase, "f", instance, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// No fakeVsockUDSServer — would block until the deadline.

	v := &JailerVMM{chrootBase: chrootBase, fcName: "f"}
	lease := Lease{Instance: instance, Slot: 0}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := v.TriggerResumeHook(ctx, lease, 1)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

// TestTriggerResumeHookGuardsRejectsBadInput covers the defense-in-depth
// guards added after the M8 PR-A review: a nil receiver, an empty
// instance, or an unconfigured chroot base must fail closed with a clear
// error rather than dialing a malformed UDS path and returning ENOENT.
func TestTriggerResumeHookGuardsRejectsBadInput(t *testing.T) {
	cases := []struct {
		name    string
		vmm     *JailerVMM
		lease   Lease
		wantSub string
	}{
		{
			name:    "nil receiver",
			vmm:     nil,
			lease:   Lease{Instance: "i", Slot: 0},
			wantSub: "nil receiver",
		},
		{
			name:    "empty instance",
			vmm:     &JailerVMM{chrootBase: "/tmp", fcName: "f"},
			lease:   Lease{Instance: "", Slot: 0},
			wantSub: "empty instance",
		},
		{
			name:    "empty chroot base",
			vmm:     &JailerVMM{chrootBase: "", fcName: "f"},
			lease:   Lease{Instance: "i", Slot: 0},
			wantSub: "chrootBase not configured",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.vmm.TriggerResumeHook(context.Background(), tc.lease, 1)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantSub)
			}
		})
	}
}

// TestBootAttachesVsockDeviceViaConfigFile verifies that the vsock device is
// configured via the config-file JSON (top-level `vsock:` field), NOT via a
// post-start PUT /vsock call. FC's /vsock endpoint is pre-boot only and
// rejects post-start requests with 400 "operation is not supported after
// starting the microVM" — the config-file path is the only legal way to
// attach a vsock device.
//
// ADR-022.
func TestBootAttachesVsockDeviceViaConfigFile(t *testing.T) {
	cfg := BuildColdBootConfig(validColdSpec(), 7)

	if cfg.VsockDevice == nil {
		t.Fatal("VsockDevice = nil, want attached (ADR-022)")
	}

	// Marshal to JSON exactly like Boot() does and confirm the top-level
	// "vsock" key carries the right shape.
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal VMConfig: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal VMConfig: %v", err)
	}
	vsockRaw, ok := raw["vsock"]
	if !ok {
		t.Fatalf("VMConfig JSON missing top-level `vsock` key; got keys: %v", keysOf(raw))
	}
	var vsock struct {
		GuestCID  uint32 `json:"guest_cid"`
		UDSSocket string `json:"uds_path"`
	}
	if err := json.Unmarshal(vsockRaw, &vsock); err != nil {
		t.Fatalf("unmarshal vsock: %v (raw=%s)", err, vsockRaw)
	}
	if vsock.GuestCID != GuestVsockCID(7) {
		t.Errorf("vsock.guest_cid = %d, want %d", vsock.GuestCID, GuestVsockCID(7))
	}
	if vsock.UDSSocket != VsockUDSSocketName {
		t.Errorf("vsock.uds_path = %q, want %q", vsock.UDSSocket, VsockUDSSocketName)
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// TestBootRestoresWake_DoesNotInvokeResumeHookOnColdBoot is in
// manager_test.go — it lives next to seedApp and the engine wiring.
var _ = "TestBootRestoresWake_DoesNotInvokeResumeHookOnColdBoot lives in manager_test.go"
