//go:build metal

// spec: §14
// adr: 171, 986 — disposable Runs on real Firecracker: cold boot, deadline,
// payload-free runtime snapshot capture, and restores that never share RNG
// state or a stale clock.
//
// Env: FAAS_TEST_KERNEL (guest kernel), optional FAAS_TEST_FC_VERSION and
// FAAS_TEST_GUEST_INIT_BIN (prebuilt guest-init; otherwise built from this
// checkout). The base image is assembled from the host's python3 so the test
// needs no registry access.
package fcvm

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// runsProbeSource reports what a Run can observe about its sandbox: a kernel
// UUID (RNG state), the wall clock, its network interfaces, and whether an
// outbound dial is possible.
const runsProbeSource = `
import os, socket, time

def main(input, context):
    with open("/proc/sys/kernel/random/uuid") as f:
        kernel_uuid = f.read().strip()
    dial = "blocked"
    try:
        s = socket.create_connection(("1.1.1.1", 80), timeout=1)
        s.close()
        dial = "connected"
    except OSError:
        pass
    return {
        "echo": input,
        "uuid": kernel_uuid,
        "urandom": os.urandom(16).hex(),
        "now": time.time(),
        "ifaces": sorted(os.listdir("/sys/class/net")),
        "dial": dial,
        "uid": os.getuid(),
    }
`

type runsProbe struct {
	Echo    json.RawMessage `json:"echo"`
	UUID    string          `json:"uuid"`
	URandom string          `json:"urandom"`
	Now     float64         `json:"now"`
	Ifaces  []string        `json:"ifaces"`
	Dial    string          `json:"dial"`
	UID     int             `json:"uid"`
}

func TestMetalDisposableRunsColdRestoreAndIsolation(t *testing.T) {
	kernel := os.Getenv("FAAS_TEST_KERNEL")
	if kernel == "" {
		t.Skip("set FAAS_TEST_KERNEL to run the disposable Runs metal test")
	}
	pythonVersion := hostPythonRuntime(t)
	work := t.TempDir()
	base := filepath.Join(work, "runs-python-base.ext4")
	layer := filepath.Join(work, "runs-execution-layer.ext4")
	buildRunsPythonBase(t, base, repoRoot(t))
	buildRunsExecutionLayer(t, layer)

	fcVersion := os.Getenv("FAAS_TEST_FC_VERSION")
	if fcVersion == "" {
		detected, err := DetectFirecrackerVersion(context.Background())
		if err != nil {
			t.Fatalf("detect firecracker version: %v", err)
		}
		fcVersion = detected
	}
	snapshotRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(snapshotRoot, "snap"), 0o2770); err != nil {
		t.Fatalf("create snapshot root: %v", err)
	}
	store, err := storage.NewLocalStorageBackend(snapshotRoot)
	if err != nil {
		t.Fatalf("create snapshot storage: %v", err)
	}
	m := NewManager(wire.ExecRunner{}, newMetalVMM(t, 30*time.Second).WithStorage(store),
		Paths{Kernel: kernel}, fcVersion, slog.New(slog.NewTextHandler(testLogWriter{t}, nil)), nil)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	names := []string{"runs-cold", "runs-timeout", "runs-capture", "runs-restore-a", "runs-restore-b"}
	t.Cleanup(func() {
		for _, name := range names {
			_ = m.Destroy(context.Background(), name)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	wake := func(instance string, snapshot *Snapshot) time.Duration {
		t.Helper()
		started := time.Now()
		if _, err := m.WakeExecution(ctx, ExecutionWakeRequest{
			Instance: instance, AccountID: "acct-runs-metal", Plan: api.PlanPro,
			Runtime: string(pythonVersion), KernelKey: kernel, BaseKey: base, LayerKey: layer,
			Snapshot: snapshot, VcpuCount: api.ExecutionVCPUCount, MemSizeMiB: 256, CPUMillicores: 1000,
		}); err != nil {
			t.Fatalf("WakeExecution %s: %v", instance, err)
		}
		return time.Since(started)
	}
	run := func(instance string, timeoutMS int, source string) executionproto.Result {
		t.Helper()
		result, err := m.ExecuteExecution(ctx, instance, executionproto.Request{
			Version: executionproto.Version, ExecutionID: instance, Runtime: pythonVersion,
			Source: source, Input: json.RawMessage(`{"n":7}`), TimeoutMS: timeoutMS,
			MaxOutput: 1 << 20, NetworkMode: api.ExecutionNetworkNone,
		})
		if err != nil {
			t.Fatalf("ExecuteExecution %s: %v", instance, err)
		}
		return result
	}
	probe := func(instance string, result executionproto.Result) runsProbe {
		t.Helper()
		if result.Status != api.ExecutionStatusSucceeded {
			t.Fatalf("%s status=%s failure=%s/%s stderr=%q", instance, result.Status, result.FailureCode, result.FailureMessage, result.Stderr)
		}
		var got runsProbe
		if err := json.Unmarshal(result.Result, &got); err != nil {
			t.Fatalf("%s result %q: %v", instance, result.Result, err)
		}
		if string(got.Echo) != `{"n":7}` {
			t.Fatalf("%s echoed input %s", instance, got.Echo)
		}
		if got.Dial != "blocked" || len(got.Ifaces) != 1 || got.Ifaces[0] != "lo" {
			t.Fatalf("%s network = ifaces %v dial %s, want loopback only and no egress", instance, got.Ifaces, got.Dial)
		}
		if got.UID == 0 {
			t.Fatalf("%s caller code ran as root", instance)
		}
		if skew := time.Since(time.Unix(0, int64(got.Now*float64(time.Second)))); skew < -5*time.Second || skew > 30*time.Second {
			t.Fatalf("%s guest clock skew %v, want a stepped clock", instance, skew)
		}
		return got
	}

	coldBoot := wake("runs-cold", nil)
	cold := probe("runs-cold", run("runs-cold", 20_000, runsProbeSource))
	t.Logf("cold boot to ready: %v", coldBoot)

	// The guest's and the host's deadlines are the same instant. Either the
	// guest reports timed_out, or vmmd's bounded exchange ends first and the
	// scheduler's sweeper records timed_out. Both must end and destroy the VM.
	wake("runs-timeout", nil)
	timeoutStarted := time.Now()
	timedOut, timeoutErr := m.ExecuteExecution(ctx, "runs-timeout", executionproto.Request{
		Version: executionproto.Version, ExecutionID: "runs-timeout", Runtime: pythonVersion,
		Source: "import time\ndef main(input, context):\n    time.sleep(30)\n", Input: json.RawMessage(`null`),
		TimeoutMS: 1500, MaxOutput: 1 << 20, NetworkMode: api.ExecutionNetworkNone,
	})
	if timeoutErr == nil && timedOut.Status != api.ExecutionStatusTimedOut {
		t.Fatalf("sleeping Run status = %s, want timed_out", timedOut.Status)
	}
	if elapsed := time.Since(timeoutStarted); elapsed > 10*time.Second {
		t.Fatalf("sleeping Run ended after %v, want the 1.5 s deadline enforced", elapsed)
	}

	// Capture: a payload-free guest parked as the runtime snapshot.
	wake("runs-capture", nil)
	const memKey = "snap/runs-metal/captures/c1/mem"
	const vmstateKey = "snap/runs-metal/captures/c1/vmstate"
	info, err := m.Park(ctx, "runs-capture", SnapshotSpec{
		StorageKey: memKey, VMStateStorageKey: vmstateKey, VMStatePath: filepath.Join(t.TempDir(), "vmstate"),
	})
	if err != nil {
		t.Fatalf("capture park: %v", err)
	}
	snapshot := &Snapshot{
		FCVersion: fcVersion, StorageKey: memKey, VMStateStorageKey: vmstateKey,
		Networkless: true, MemBytes: info.MemBytes,
	}

	restoreA := wake("runs-restore-a", snapshot)
	a := probe("runs-restore-a", run("runs-restore-a", 20_000, runsProbeSource))
	restoreB := wake("runs-restore-b", snapshot)
	b := probe("runs-restore-b", run("runs-restore-b", 20_000, runsProbeSource))
	t.Logf("restore to ready: %v and %v (cold boot %v)", restoreA, restoreB, coldBoot)

	// Spec §11 V6: two Runs restored from one snapshot never share RNG state.
	if a.UUID == b.UUID || a.URandom == b.URandom {
		t.Fatalf("restores share RNG state: uuid %s/%s urandom %s/%s", a.UUID, b.UUID, a.URandom, b.URandom)
	}
	if a.UUID == cold.UUID {
		t.Fatalf("restored Run repeated the cold Run's RNG output")
	}

	// Every Run VM is destroyed when its Run ends; nothing stays resident.
	for _, inst := range m.liveInstances() {
		for _, name := range names {
			if inst.Lease.Instance == name {
				t.Fatalf("Run VM %s is still live after its Run", name)
			}
		}
	}
	for _, name := range names {
		matches, _ := filepath.Glob(filepath.Join("/sys/fs/cgroup", "*", "*"+name+"*"))
		if len(matches) != 0 {
			t.Fatalf("Run %s left cgroups behind: %v", name, matches)
		}
	}
}

// hostPythonRuntime maps the host python3 to a Runs runtime.
func hostPythonRuntime(t *testing.T) api.ExecutionRuntime {
	t.Helper()
	out, err := exec.Command("python3", "-c", "import sys; print(f'{sys.version_info[0]}{sys.version_info[1]}')").Output()
	if err != nil {
		t.Skipf("host python3 unavailable: %v", err)
	}
	runtime := api.ExecutionRuntime("python" + strings.TrimSpace(string(out)))
	if !runtime.Valid() {
		t.Skipf("host python3 %s is not a Runs runtime", runtime)
	}
	return runtime
}

var lddPathRE = regexp.MustCompile(`(/[^\s]+\.so[^\s]*)`)

// buildRunsPythonBase assembles a read-only base with guest-init as /sbin/init
// and the host's python3 (binary, shared libraries, standard library) at the
// paths the Run executor uses.
func buildRunsPythonBase(t *testing.T, dst, repoRoot string) {
	t.Helper()
	tree := t.TempDir()
	for _, dir := range []string{"sbin", "dev", "proc", "sys", "tmp", "etc/faas", "overlay", "usr/local/bin"} {
		if err := os.MkdirAll(filepath.Join(tree, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(tree, "tmp"), 0o1777); err != nil {
		t.Fatal(err)
	}
	initBin := filepath.Join(tree, "sbin", "init")
	if prebuilt := os.Getenv("FAAS_TEST_GUEST_INIT_BIN"); prebuilt != "" {
		if err := bbCopyFile(prebuilt, initBin); err != nil {
			t.Fatalf("copy guest-init: %v", err)
		}
	} else {
		build := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", initBin, ".")
		build.Dir = filepath.Join(repoRoot, "guest", "init")
		build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build guest-init: %v\n%s", err, out)
		}
	}

	python, err := filepath.EvalSymlinks("/usr/bin/python3")
	if err != nil {
		t.Fatalf("resolve host python3: %v", err)
	}
	copyIntoTree(t, tree, python)
	if err := os.Symlink(python, filepath.Join(tree, "usr/local/bin/python3")); err != nil {
		t.Fatal(err)
	}
	stdlib := filepath.Join("/usr/lib", filepath.Base(python))
	skip := map[string]bool{"test": true, "idlelib": true, "tkinter": true, "turtledemo": true, "ensurepip": true, "lib2to3": true}
	sharedObjects := []string{python}
	err = filepath.WalkDir(stdlib, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && skip[d.Name()] {
			return filepath.SkipDir
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		copyIntoTree(t, tree, path)
		if strings.HasSuffix(path, ".so") {
			sharedObjects = append(sharedObjects, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("copy python stdlib %s: %v", stdlib, err)
	}
	copied := map[string]bool{}
	for _, object := range sharedObjects {
		out, err := exec.Command("ldd", object).Output()
		if err != nil {
			continue // static or not a dynamic object
		}
		for _, lib := range lddPathRE.FindAllString(string(out), -1) {
			if copied[lib] {
				continue
			}
			copied[lib] = true
			real, err := filepath.EvalSymlinks(lib)
			if err != nil {
				t.Fatalf("resolve %s: %v", lib, err)
			}
			copyIntoTree(t, tree, real)
			if real != lib {
				if err := os.MkdirAll(filepath.Join(tree, filepath.Dir(lib)), 0o755); err != nil {
					t.Fatal(err)
				}
				_ = os.Symlink(real, filepath.Join(tree, lib))
			}
		}
	}
	mkfsRunsImage(t, tree, dst, 512)
}

// buildRunsExecutionLayer builds the payload-free drive1 imaged stages in
// production: only the execution marker under the overlay upper.
func buildRunsExecutionLayer(t *testing.T, dst string) {
	t.Helper()
	tree := t.TempDir()
	marker := filepath.Join(tree, "upper", "etc", "faas", "execution.json")
	for _, dir := range []string{filepath.Dir(marker), filepath.Join(tree, "work"), filepath.Join(tree, "merged")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(marker, []byte(`{"kind":"execution","version":1}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mkfsRunsImage(t, tree, dst, 16)
}

func copyIntoTree(t *testing.T, tree, path string) {
	t.Helper()
	dst := filepath.Join(tree, path)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := bbCopyFile(path, dst); err != nil {
		t.Fatalf("copy %s: %v", path, err)
	}
	if info, err := os.Stat(path); err == nil {
		_ = os.Chmod(dst, info.Mode().Perm())
	}
}

func mkfsRunsImage(t *testing.T, tree, dst string, sizeMB int) {
	t.Helper()
	out, err := exec.Command("mkfs.ext4", "-q", "-F", "-O", "^has_journal", "-d", tree, dst, fmt.Sprintf("%dM", sizeMB)).CombinedOutput()
	if err != nil {
		t.Fatalf("mkfs.ext4 %s: %v\n%s", dst, err, out)
	}
	if err := os.Chmod(dst, 0o644); err != nil {
		t.Fatal(err)
	}
}
