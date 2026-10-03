//go:build metal

// adr: 481
package fcvm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// spec: §6.2 invariant 5 — two instances restored from one snapshot never
// share an RNG stream. TestMetalRestoreUserspaceRNGDiffers restores ONE
// snapshot of a real Node or Python process twice and asserts the values its
// in-process generators return differ (ADR-481). The kernel-level V6 test
// cannot see this failure: before ADR-481 /dev/urandom was unique on every
// wake while Node's randomUUID, randomBytes and Math.random and Python's
// random and ssl replayed identical values (production, 2026-09-22).
//
// The control subtest turns the reseed off and requires the values to
// repeat, which proves the fixture captures generator state in the snapshot
// and the assertion can fail.
//
// The runtime comes from a root filesystem tarball of the production runner
// base (for example `docker export` of node:22-alpine, or wolfi-base with
// python-3.13 installed):
//
//	FAAS_TEST_RNG_NODE_ROOTFS=/var/tmp/node22.tar \
//	FAAS_TEST_RNG_PYTHON_ROOTFS=/var/tmp/python313.tar \
//	make test-metal PKGS=./pkg/fcvm RUN_REGEX=TestMetalRestoreUserspaceRNGDiffers
func TestMetalRestoreUserspaceRNGDiffers(t *testing.T) {
	kernel := os.Getenv("FAAS_TEST_KERNEL")
	if kernel == "" {
		kernel = "/srv/fc/base/vmlinux-6.1.134"
	}
	if _, err := os.Stat(kernel); err != nil {
		t.Skipf("kernel %s unavailable: %v (set FAAS_TEST_KERNEL)", kernel, err)
	}
	runtimes := []struct {
		name, rootfsEnv, script, file string
		entrypoint                    []string
		// replayed names the fields that must repeat when the reseed is off.
		replayed []string
	}{
		{
			name: "node", rootfsEnv: "FAAS_TEST_RNG_NODE_ROOTFS", file: "srv/rng-app.js", script: rngNodeApp,
			entrypoint: []string{"/usr/local/bin/node", "/srv/rng-app.js"}, replayed: []string{"uuid", "bytes", "math"},
		},
		{
			name: "python", rootfsEnv: "FAAS_TEST_RNG_PYTHON_ROOTFS", file: "srv/rng-app.py", script: rngPythonApp,
			entrypoint: []string{"/usr/bin/python3", "/srv/rng-app.py"}, replayed: []string{"random", "ssl"},
		},
	}
	ran := 0
	for _, rt := range runtimes {
		tarball := os.Getenv(rt.rootfsEnv)
		if tarball == "" {
			continue
		}
		ran++
		for _, reseed := range []bool{true, false} {
			name := rt.name + "/reseed"
			if !reseed {
				name = rt.name + "/reseed-off-control"
			}
			t.Run(name, func(t *testing.T) {
				env := map[string]string{}
				if !reseed {
					env["GREGALE_RESTORE_RESEED"] = "off"
				}
				first, second := restoreSnapshotTwice(t, kernel, tarball, rt.file, rt.script, rt.entrypoint, env)
				t.Logf("restore 1: %v", first)
				t.Logf("restore 2: %v", second)
				if reseed {
					for field, value := range first {
						if field != "pid" && second[field] == value {
							t.Errorf("%s: %s = %v after both restores of one snapshot; the userspace RNG replayed", rt.name, field, value)
						}
					}
					return
				}
				for _, field := range rt.replayed {
					if first[field] != second[field] {
						t.Errorf("control: %s differs (%v vs %v) with the reseed off; the fixture no longer captures generator state, so the reseed assertion proves nothing", field, first[field], second[field])
					}
				}
			})
		}
	}
	if ran == 0 {
		t.Skip("set FAAS_TEST_RNG_NODE_ROOTFS and/or FAAS_TEST_RNG_PYTHON_ROOTFS to a runner root filesystem tarball")
	}
}

// restoreSnapshotTwice cold-boots the app, parks it into one snapshot, then
// restores that snapshot twice (destroying the first instance in between)
// and returns the JSON each restored process served on its first request.
func restoreSnapshotTwice(t *testing.T, kernel, tarball, file, script string, entrypoint []string, env map[string]string) (map[string]any, map[string]any) {
	t.Helper()
	dir := benchFixtureDir(t)
	base := filepath.Join(dir, "rng-base.ext4")
	layer := filepath.Join(dir, "rng-layer.ext4")
	if err := buildRuntimeBaseExt4(base, repoRoot(t), tarball, file, script, entrypoint, env); err != nil {
		t.Fatalf("build runtime base: %v", err)
	}
	if err := buildV6LayerExt4Size(layer, 64); err != nil {
		t.Fatalf("build layer: %v", err)
	}
	fcVersion, err := DetectFirecrackerVersion(context.Background())
	if err != nil {
		t.Fatalf("detect firecracker version: %v", err)
	}
	snapshotRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(snapshotRoot, "snap"), 0o2770); err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewLocalStorageBackend(snapshotRoot)
	if err != nil {
		t.Fatal(err)
	}
	vmm := newMetalVMM(t, 60*time.Second).WithStorage(store)
	stageBenchMountHelper(t, vmm)
	m := NewManager(wire.ExecRunner{}, vmm, Paths{Kernel: kernel}, fcVersion, nil, nil)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	const instance = "restore-rng"
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })

	const memMiB = 256
	if _, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance: instance, Plan: "pro", BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: memMiB,
	}); err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	snap := &Snapshot{
		FCVersion:         fcVersion,
		StorageKey:        "snap/restore-rng/mem",
		VMStateStorageKey: "snap/restore-rng/vmstate",
		VMStatePath:       filepath.Join(t.TempDir(), "vmstate"),
	}
	if _, err := m.Park(ctx, instance, SnapshotSpec{
		VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey,
	}); err != nil {
		t.Fatalf("park: %v", err)
	}
	restore := func(round int) map[string]any {
		t.Helper()
		out, err := m.Wake(ctx, WakeRequest{
			Instance: instance, Plan: "pro", BaseKey: base, LayerKey: layer,
			VcpuCount: 1, MemSizeMiB: memMiB, Snapshot: snap,
		})
		if err != nil {
			t.Fatalf("restore %d: %v", round, err)
		}
		if out.Method != WakeRestore {
			t.Fatalf("restore %d: method = %s, want restore (a cold boot would hide a replay)", round, out.Method)
		}
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Get(fmt.Sprintf("http://%s:8080/", out.Lease.HostIP))
		if err != nil {
			t.Fatalf("restore %d: request: %v", round, err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		var values map[string]any
		if err := json.Unmarshal(body, &values); err != nil {
			t.Fatalf("restore %d: response %q: %v", round, body, err)
		}
		return values
	}
	first := restore(1)
	if err := m.Destroy(ctx, instance); err != nil {
		t.Fatalf("destroy between restores: %v", err)
	}
	return first, restore(2)
}

// buildRuntimeBaseExt4 unpacks a runner root filesystem tarball, adds the
// guest-init built from this tree, the probe app and its manifest, and packs
// the result as drive0.
func buildRuntimeBaseExt4(dst, repoRoot, tarball, file, script string, entrypoint []string, env map[string]string) error {
	work, err := os.MkdirTemp(filepath.Dir(dst), "rng-skel-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	if out, err := exec.Command("tar", "-xf", tarball, "-C", work).CombinedOutput(); err != nil {
		return fmt.Errorf("unpack %s: %w: %s", tarball, err, out)
	}
	for _, sub := range []string{"sbin", "dev", "sys", "proc", "etc/faas", "tmp", "var/tmp", "overlay", "srv"} {
		if err := os.MkdirAll(filepath.Join(work, sub), 0o755); err != nil {
			return err
		}
	}
	_ = os.Remove(filepath.Join(work, "sbin", "init"))
	build := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", filepath.Join(work, "sbin", "init"), ".")
	build.Dir = filepath.Join(repoRoot, "guest", "init")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux")
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("build guest-init: %w: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(work, file), []byte(script), 0o644); err != nil {
		return err
	}
	manifest, err := json.Marshal(map[string]any{"entrypoint": entrypoint, "port": 8080, "env": env})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(work, "etc", "faas", "app.json"), append(manifest, '\n'), 0o644); err != nil {
		return err
	}
	du, err := exec.Command("du", "-sm", work).Output()
	if err != nil {
		return fmt.Errorf("size rootfs: %w", err)
	}
	var usedMB int
	if _, err := fmt.Sscan(string(du), &usedMB); err != nil {
		return fmt.Errorf("parse du %q: %w", du, err)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	if err := f.Truncate(int64(usedMB+128) << 20); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if out, err := exec.Command("mkfs.ext4", "-q", "-O", "^has_journal", "-d", work, "-L", "faas-rng", "-F", dst).CombinedOutput(); err != nil {
		return fmt.Errorf("mkfs.ext4: %w: %s", err, out)
	}
	return os.Chmod(dst, 0o644)
}

// The probe apps draw from every in-process generator at startup, so the
// generator state is in the snapshot, then report fresh draws per request.
const rngNodeApp = `const http = require("http");
const crypto = require("crypto");
crypto.randomUUID(); crypto.randomBytes(16); Math.random();
http.createServer((req, res) => {
  res.setHeader("content-type", "application/json");
  res.end(JSON.stringify({
    uuid: crypto.randomUUID(),
    bytes: crypto.randomBytes(16).toString("hex"),
    math: Math.random(),
    pid: process.pid,
  }));
}).listen(8080, "0.0.0.0");
`

const rngPythonApp = `import http.server, json, os, random, ssl
random.random(); ssl.RAND_bytes(16)
class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        body = json.dumps({
            "random": random.random(),
            "ssl": ssl.RAND_bytes(16).hex(),
            "pid": os.getpid(),
        }).encode()
        self.send_response(200)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *args):
        pass
http.server.HTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
`
