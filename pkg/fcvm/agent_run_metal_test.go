//go:build metal

// adr: 20261027090010 — one-shot agent runs cold-boot from the agent base with the
// platform agent layer, take their command and inputs over agentrunproto,
// stream output, and leave nothing behind. Each case boots a fresh VM.
package fcvm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/agentrunproto"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// agentRunLayer builds the platform agent layer: drive1 whose upper holds
// only the agent marker, plus room for the run's writes.
func agentRunLayer(t *testing.T) string {
	t.Helper()
	skel := t.TempDir()
	marker := filepath.Join(skel, "upper", "etc", "faas", "agent.json")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(`{"kind":"agent","version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	layer := filepath.Join(t.TempDir(), "agent-layer.ext4")
	if out, err := exec.Command("truncate", "-s", "512M", layer).CombinedOutput(); err != nil {
		t.Fatalf("truncate: %v %s", err, out)
	}
	if out, err := exec.Command("mkfs.ext4", "-q", "-O", "^has_journal", "-d", skel, "-L", "agent-layer", "-F", layer).CombinedOutput(); err != nil {
		t.Fatalf("mkfs.ext4: %v %s", err, out)
	}
	return layer
}

func TestMetalAgentRun(t *testing.T) {
	kernel := os.Getenv("FAAS_TEST_KERNEL")
	base := os.Getenv("FAAS_TEST_AGENT_RUN_BASE")
	if kernel == "" || base == "" {
		t.Skip("set FAAS_TEST_KERNEL and FAAS_TEST_AGENT_RUN_BASE (an agent-base ext4 with this checkout's guest-init)")
	}
	withCgroupRootAt(t, "/sys/fs/cgroup")
	// Preset snapshots publish memory, state and the frozen drive1 here.
	snapRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(snapRoot, "snap"), 0o2770); err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewLocalStorageBackend(snapRoot)
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(wire.ExecRunner{}, newMetalVMM(t, 120*time.Second).WithStorage(store), Paths{Kernel: kernel}, os.Getenv("FAAS_TEST_FC_VERSION"), slog.Default(), nil)
	layer := agentRunLayer(t)
	ctx := context.Background()

	run := func(t *testing.T, id string, memMiB int, req agentrunproto.Request, cancel <-chan struct{}, input ...string) (agentrunproto.Result, time.Duration) {
		t.Helper()
		wake := AgentRunWakeRequest{Instance: id, AccountID: "acct-agent-run", Plan: api.PlanPro,
			BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: memMiB, CPUMillicores: 1000}
		if len(input) == 1 {
			wake.InputKey = input[0]
		}
		inst, err := m.WakeAgentRun(ctx, wake)
		if err != nil {
			t.Fatalf("wake %s: %v", id, err)
		}
		t.Cleanup(func() { _ = m.Destroy(context.Background(), id) })
		if inst.Method != WakeColdBoot || !inst.Lease.Networkless {
			t.Fatalf("wake %s: method %s, networkless %v", id, inst.Method, inst.Lease.Networkless)
		}
		start := time.Now()
		result, err := m.RunAgent(ctx, id, req, nil, cancel)
		if err != nil {
			t.Fatalf("run %s: %v", id, err)
		}
		if live := m.LiveCount(); live != 0 {
			t.Fatalf("run %s left %d live VMs", id, live)
		}
		return result, time.Since(start)
	}
	request := func(timeout int, script string) agentrunproto.Request {
		return agentrunproto.Request{Version: agentrunproto.Version, RunID: "run-metal", Command: []string{"sh", "-c", script},
			Env:        map[string]string{"GREGALE_TEST_MARK": "mark-1"},
			Files:      []agentrunproto.File{{Path: "prompt.txt", Content: []byte("triage PR 7")}, {Path: "steps/plan/notes.md", Content: []byte("plan")}},
			TimeoutSec: timeout, MaxLogBytes: 1 << 20, MaxResultBytes: 64 << 10, MaxOutputFiles: 8, MaxOutputBytes: 1 << 20}
	}

	t.Run("succeeds with outputs", func(t *testing.T) {
		script := `set -e
cat /input/prompt.txt; echo; cat /input/steps/plan/notes.md; echo
echo "uid=$(id -u) home=$HOME cwd=$(pwd) mark=$GREGALE_TEST_MARK"
if touch /input/x 2>/dev/null; then echo "input is writable"; exit 9; fi
echo warn >&2
mkdir -p /work/output/reports
echo '{"verdict":"ok"}' > /work/output/result.json
echo report > /work/output/reports/a.md
ln -s /etc/shadow /work/output/link`
		result, _ := run(t, "agent-run-ok", 1024, request(60, script), nil)
		if result.Status != agentrunproto.StatusSucceeded {
			t.Fatalf("status %s exit %d failure %q\nstdout %s\nstderr %s", result.Status, result.ExitCode, result.FailureCode, result.Stdout, result.Stderr)
		}
		out := string(result.Stdout)
		for _, want := range []string{"triage PR 7", "plan", "uid=1000", "cwd=/work", "mark=mark-1"} {
			if !strings.Contains(out, want) {
				t.Errorf("stdout lacks %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, "home=/tmp") || strings.TrimSpace(string(result.Stderr)) != "warn" {
			t.Errorf("home or stderr wrong: %s / %q", out, result.Stderr)
		}
		var verdict map[string]string
		if json.Unmarshal(result.Result, &verdict) != nil || verdict["verdict"] != "ok" {
			t.Errorf("result %s", result.Result)
		}
		if len(result.Files) != 1 || result.Files[0].Name != "reports/a.md" || string(result.Files[0].Content) != "report\n" {
			t.Errorf("files %+v (the symlink must not be followed)", result.Files)
		}
	})

	t.Run("unpacks the checkout", func(t *testing.T) {
		image, checkout := metalInputImage(t)
		req := request(60, `set -e
cat /input/repo/README.md; echo
test -x /input/repo/bin/run
echo "owner=$(stat -c %u /input/repo/README.md) link=$(readlink /input/repo/docs/readme)"
touch /input/repo/edited`)
		req.Checkout = &checkout
		result, _ := run(t, "agent-run-checkout", 1024, req, nil, image)
		if result.Status != agentrunproto.StatusSucceeded {
			t.Fatalf("status %s exit %d failure %q\nstdout %s\nstderr %s", result.Status, result.ExitCode, result.FailureCode, result.Stdout, result.Stderr)
		}
		for _, want := range []string{"hello repo", "owner=1000", "link=../README.md"} {
			if !strings.Contains(string(result.Stdout), want) {
				t.Errorf("stdout lacks %q:\n%s", want, result.Stdout)
			}
		}
		wrong := checkout
		wrong.SHA256 = strings.Repeat("0", 64)
		req.Checkout = &wrong
		result, _ = run(t, "agent-run-checkout-wrong", 1024, req, nil, image)
		if result.Status != agentrunproto.StatusFailed || result.FailureCode != agentrunproto.FailureCheckout {
			t.Fatalf("a mismatched drive: %+v", result)
		}
	})

	// restored runs one VM from a snapshot and reports how it woke.
	restored := func(t *testing.T, id string, snap *Snapshot, req agentrunproto.Request, input string) (agentrunproto.Result, *Instance) {
		t.Helper()
		begun := time.Now()
		defer func() { t.Logf("%s: wake to result in %d ms", id, time.Since(begun).Milliseconds()) }()
		inst, err := m.WakeAgentRun(ctx, AgentRunWakeRequest{Instance: id, AccountID: "acct-agent-run", Plan: api.PlanPro,
			BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: 1024, CPUMillicores: 1000, InputKey: input, Snapshot: snap})
		if err != nil {
			t.Fatalf("wake %s: %v", id, err)
		}
		t.Cleanup(func() { _ = m.Destroy(context.Background(), id) })
		result, err := m.RunAgent(ctx, id, req, nil, nil)
		if err != nil {
			t.Fatalf("run %s: %v", id, err)
		}
		if live := m.LiveCount(); live != 0 {
			t.Fatalf("run %s left %d live VMs", id, live)
		}
		return result, inst
	}

	t.Run("restores from a preset snapshot", func(t *testing.T) {
		snap, info, err := m.CaptureAgentRunSnapshot(ctx, AgentRunCapture{Namespace: "agent-preset-metal", CaptureID: "capture-1",
			Wake: AgentRunWakeRequest{Instance: "agent-run-capture", AccountID: "acct-agent-run", Plan: api.PlanPro,
				BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: 1024, CPUMillicores: 1000}})
		if err != nil {
			t.Fatalf("capture: %v", err)
		}
		if info.MemBytes == 0 || m.LiveCount() != 0 {
			t.Fatalf("capture info %+v, live %d", info, m.LiveCount())
		}
		image, checkout := metalInputImage(t)
		script := `set -e
cat /input/repo/README.md; echo
echo "rand=$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n')"
echo "now=$(date +%s)"`
		fields := func(t *testing.T, result agentrunproto.Result) map[string]string {
			t.Helper()
			if result.Status != agentrunproto.StatusSucceeded {
				t.Fatalf("status %s exit %d failure %q\nstdout %s\nstderr %s", result.Status, result.ExitCode, result.FailureCode, result.Stdout, result.Stderr)
			}
			out := map[string]string{}
			for _, line := range strings.Split(string(result.Stdout), "\n") {
				if k, v, ok := strings.Cut(line, "="); ok {
					out[k] = v
				}
			}
			if !strings.Contains(string(result.Stdout), "hello repo") {
				t.Fatalf("checkout missing after restore:\n%s", result.Stdout)
			}
			return out
		}
		var seen []string
		for _, id := range []string{"agent-run-restore-1", "agent-run-restore-2"} {
			req := request(60, script)
			req.Checkout = &checkout
			result, inst := restored(t, id, snap, req, image)
			if inst.Method != WakeRestore {
				t.Fatalf("%s woke by %s, want a restore: %q (%s)", id, inst.Method, inst.RestoreError, inst.RestoreFallbackReason)
			}
			out := fields(t, result)
			now, err := strconv.ParseInt(out["now"], 10, 64)
			if err != nil || time.Since(time.Unix(now, 0)).Abs() > 10*time.Second {
				t.Fatalf("%s clock %q is not stepped to the host's\nstdout %q\nstderr %q", id, out["now"], result.Stdout, result.Stderr)
			}
			t.Logf("%s: woke in %d ms, stdout %q", id, inst.RestoreMs, result.Stdout)
			seen = append(seen, out["rand"])
		}
		if seen[0] == "" || seen[0] == seen[1] {
			t.Fatalf("two restores share an RNG stream: %q", seen)
		}
		if result, inst := restored(t, "agent-run-restore-plain", snap, request(60, `test ! -e /input/repo && echo plain`), ""); inst.Method != WakeRestore ||
			result.Status != agentrunproto.StatusSucceeded || !strings.Contains(string(result.Stdout), "plain") {
			t.Fatalf("restore without a checkout: %s %q %+v", inst.Method, inst.RestoreError, result)
		}
		stale := *snap
		stale.FCVersion = "0.0.0"
		if result, inst := restored(t, "agent-run-restore-stale", &stale, request(60, `echo cold`), ""); inst.Method != WakeColdBoot ||
			result.Status != agentrunproto.StatusSucceeded {
			t.Fatalf("a stale snapshot: %s %+v", inst.Method, result)
		}
	})

	t.Run("failure returns no outputs", func(t *testing.T) {
		result, _ := run(t, "agent-run-fail", 1024, request(60, `echo '{"a":1}' > /work/output/result.json; exit 3`), nil)
		if result.Status != agentrunproto.StatusFailed || result.ExitCode != 3 || len(result.Result) != 0 {
			t.Fatalf("result %+v", result)
		}
	})

	t.Run("times out", func(t *testing.T) {
		result, took := run(t, "agent-run-timeout", 1024, request(3, `trap '' TERM; sleep 600`), nil)
		if result.Status != agentrunproto.StatusTimedOut {
			t.Fatalf("result %+v", result)
		}
		if limit := time.Duration(3+2*api.AgentRunStopGraceSec+30) * time.Second; took > limit {
			t.Fatalf("timed-out run took %s, over %s", took, limit)
		}
	})

	t.Run("cancels", func(t *testing.T) {
		cancel := make(chan struct{})
		var once sync.Once
		time.AfterFunc(5*time.Second, func() { once.Do(func() { close(cancel) }) })
		result, _ := run(t, "agent-run-cancel", 1024, request(300, `sleep 600`), cancel)
		if result.Status != agentrunproto.StatusCancelled {
			t.Fatalf("result %+v", result)
		}
	})

	t.Run("runs out of memory", func(t *testing.T) {
		script := `node -e 'const a = []; for (;;) a.push(Buffer.alloc(32 << 20, 1))'`
		result, _ := run(t, "agent-run-oom", 512, request(120, script), nil)
		if result.Status != agentrunproto.StatusOutOfMemory {
			t.Fatalf("status %s exit %d signal %d\nstderr %s", result.Status, result.ExitCode, result.Signal, result.Stderr)
		}
	})

	// The networked boot shares the jailer chroot wait with the networkless
	// device setup; an ordinary app VM from the same base must still serve.
	t.Run("networked app still boots", func(t *testing.T) {
		skel := t.TempDir()
		manifest := filepath.Join(skel, "upper", "etc", "faas", "app.json")
		if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
			t.Fatal(err)
		}
		app := `{"entrypoint":["python3","-m","http.server","8080"],"port":8080,"working_dir":"/work"}`
		if err := os.WriteFile(manifest, []byte(app), 0o644); err != nil {
			t.Fatal(err)
		}
		appLayer := filepath.Join(t.TempDir(), "app-layer.ext4")
		if out, err := exec.Command("truncate", "-s", "256M", appLayer).CombinedOutput(); err != nil {
			t.Fatalf("truncate: %v %s", err, out)
		}
		if out, err := exec.Command("mkfs.ext4", "-q", "-O", "^has_journal", "-d", skel, "-F", appLayer).CombinedOutput(); err != nil {
			t.Fatalf("mkfs.ext4: %v %s", err, out)
		}
		inst, err := m.Wake(ctx, WakeRequest{Instance: "agent-base-app", AccountID: "acct-agent-run", AppID: "app-agent-base",
			Plan: api.PlanPro, BaseKey: base, LayerKey: appLayer, VcpuCount: 1, MemSizeMiB: 512})
		if err != nil {
			t.Fatalf("wake: %v", err)
		}
		t.Cleanup(func() { _ = m.Destroy(context.Background(), "agent-base-app") })
		if inst.Lease.Networkless {
			t.Fatal("an app VM came up networkless")
		}
		resp, err := firstResponse("http://"+inst.Lease.HostIP.String()+":8080/", 120*time.Second)
		if err != nil {
			t.Fatalf("app does not serve: %v", err)
		}
		_ = resp.Body.Close()
		if err := m.Destroy(ctx, "agent-base-app"); err != nil {
			t.Fatalf("destroy: %v", err)
		}
	})

	t.Run("presets start", func(t *testing.T) {
		req := request(120, `codex --version && claude --version && opencode --version`)
		req.Env = map[string]string{"DISABLE_AUTOUPDATER": "1", "USE_BUILTIN_RIPGREP": "0"}
		result, _ := run(t, "agent-run-presets", 2048, req, nil)
		if result.Status != agentrunproto.StatusSucceeded {
			t.Fatalf("status %s exit %d\nstdout %s\nstderr %s", result.Status, result.ExitCode, result.Stdout, result.Stderr)
		}
		t.Logf("preset versions:\n%s", result.Stdout)
	})
}

// metalInputImage writes a small commit archive as an input drive.
func metalInputImage(t *testing.T) (string, agentrunproto.Checkout) {
	t.Helper()
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	for _, e := range []struct {
		name, body, link string
		typ              byte
		mode             int64
	}{
		{name: "acme-app-0123456/", typ: tar.TypeDir, mode: 0o755},
		{name: "acme-app-0123456/README.md", body: "hello repo", typ: tar.TypeReg, mode: 0o644},
		{name: "acme-app-0123456/bin/run", body: "#!/bin/sh\n", typ: tar.TypeReg, mode: 0o755},
		{name: "acme-app-0123456/docs/readme", link: "../README.md", typ: tar.TypeSymlink, mode: 0o777},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Typeflag: e.typ, Mode: e.mode, Size: int64(len(e.body)), Linkname: e.link}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(archive.Bytes())
	path := filepath.Join(t.TempDir(), "agent-input.img")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(agentrunproto.WriteInputImage(f, bytes.NewReader(archive.Bytes()), int64(archive.Len()), sum), f.Close()); err != nil {
		t.Fatal(err)
	}
	return path, agentrunproto.Checkout{SHA256: hex.EncodeToString(sum[:]), Bytes: int64(archive.Len()),
		MaxExtractedBytes: 1 << 20, MaxEntries: 100}
}
