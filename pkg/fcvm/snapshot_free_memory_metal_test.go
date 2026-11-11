//go:build metal

package fcvm

// adr: 907 — how much of a capture is memory the guest had already freed, and
// what guest kernel settings change it (free page reporting order,
// init_on_free).

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestMetalSnapshotFreeMemoryBench boots a Node app with the ADR-907 balloon,
// churns its heap and child processes the way a serving app does, parks it,
// and reports for each guest kernel variant:
//
//   - the guest's free memory by buddy order just before the capture (free
//     blocks below the reporting order are never discarded, so they keep
//     their old contents and are captured);
//   - the capture's non-zero bytes, which is what is stored;
//   - the host memory of the running VM (free page reporting returns it);
//   - restore time, cold and warm page cache, and the first request after;
//   - the time of a churn request (the guest CPU cost of the variant).
//
// Opt-in and slow. FAAS_TEST_FREE_MEMORY_NODE_ROOTFS names a Node runner root
// filesystem tarball (as FAAS_TEST_RNG_NODE_ROOTFS). Variants are
// name=extra-boot-args pairs separated by ";" in FAAS_FREE_MEMORY_VARIANTS;
// FAAS_FREE_MEMORY_ROUNDS repeats them interleaved, FAAS_FREE_MEMORY_MEM_MIB
// sets guest RAM (default 1024) and FAAS_FREE_MEMORY_RESTORES the restores
// per measurement (default 5). Set FAAS_TEST_FREE_PAGE_REPORTING=1 and run on
// Firecracker 1.14+ with the production guest kernel.
func TestMetalSnapshotFreeMemoryBench(t *testing.T) {
	tarball := os.Getenv("FAAS_TEST_FREE_MEMORY_NODE_ROOTFS")
	if tarball == "" {
		t.Skip("set FAAS_TEST_FREE_MEMORY_NODE_ROOTFS to a Node runner root filesystem tarball")
	}
	kernel := os.Getenv("FAAS_TEST_KERNEL")
	if _, err := os.Stat(kernel); kernel == "" || err != nil {
		t.Skipf("set FAAS_TEST_KERNEL to the production guest kernel (%q: %v)", kernel, err)
	}
	memMiB := envInt(t, "FAAS_FREE_MEMORY_MEM_MIB", 1024)
	rounds := envInt(t, "FAAS_FREE_MEMORY_ROUNDS", 1)
	restores := envInt(t, "FAAS_FREE_MEMORY_RESTORES", 5)
	variants := freeMemoryVariants(t)

	dir := benchFixtureDir(t)
	base := filepath.Join(dir, "freemem-base.ext4")
	layer := filepath.Join(dir, "freemem-layer.ext4")
	if err := buildRuntimeBaseExt4(base, repoRoot(t), tarball, "srv/churn.js", freeMemoryNodeApp,
		[]string{"/usr/local/bin/node", "--expose-gc", "/srv/churn.js"}, nil); err != nil {
		t.Fatalf("build runtime base: %v", err)
	}
	if err := buildV6LayerExt4Size(layer, 64); err != nil {
		t.Fatalf("build layer: %v", err)
	}
	fcVersion, err := DetectFirecrackerVersion(context.Background())
	if err != nil {
		t.Fatalf("detect firecracker version: %v", err)
	}
	if !FreePageReportingSupported(fcVersion) || os.Getenv("FAAS_TEST_FREE_PAGE_REPORTING") != "1" {
		t.Logf("WARNING: free page reporting is off (firecracker %s, FAAS_TEST_FREE_PAGE_REPORTING=%q); production has it on",
			fcVersion, os.Getenv("FAAS_TEST_FREE_PAGE_REPORTING"))
	}
	snapshotRoot := filepath.Join(dir, "store")
	if err := os.MkdirAll(filepath.Join(snapshotRoot, "snap"), 0o2770); err != nil {
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
	previousProfile := guestTimerProfile
	t.Cleanup(func() { guestTimerProfile = previousProfile })

	for round := range rounds {
		for _, v := range variants {
			guestTimerProfile = previousProfile + v.args
			freeMemoryRun(t, m, store, fcVersion, base, layer, memMiB, restores, round, v.name)
		}
	}
}

type freeMemoryVariant struct{ name, args string }

func freeMemoryVariants(t *testing.T) []freeMemoryVariant {
	raw := os.Getenv("FAAS_FREE_MEMORY_VARIANTS")
	if raw == "" {
		raw = "base=;order5=page_reporting.page_reporting_order=5;order3=page_reporting.page_reporting_order=3;" +
			"order0=page_reporting.page_reporting_order=0;iof=init_on_free=1"
	}
	var out []freeMemoryVariant
	for _, part := range strings.Split(raw, ";") {
		name, args, ok := strings.Cut(part, "=")
		if !ok || name == "" {
			t.Fatalf("FAAS_FREE_MEMORY_VARIANTS: %q is not name=args", part)
		}
		if args != "" {
			args += " "
		}
		out = append(out, freeMemoryVariant{name: name, args: args})
	}
	return out
}

func freeMemoryRun(t *testing.T, m *Manager, store *storage.LocalStorageBackend, fcVersion, base, layer string, memMiB, restores, round int, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	instance := fmt.Sprintf("freemem-%s-%d", name, round)
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })
	label := fmt.Sprintf("round %d %s", round, name)

	cold, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance: instance, Plan: "pro", BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: memMiB,
	})
	if err != nil {
		t.Fatalf("%s: cold boot: %v", label, err)
	}
	url := fmt.Sprintf("http://%s:8080", cold.Lease.HostIP)
	var churn []time.Duration
	// FAAS_FREE_MEMORY_CHURN churn requests before the capture (default 6);
	// 0 captures right after readiness, as a new deployment's first park
	// does.
	churnRequests := 6
	if raw := os.Getenv("FAAS_FREE_MEMORY_CHURN"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			t.Fatalf("FAAS_FREE_MEMORY_CHURN=%q: want a whole number", raw)
		}
		churnRequests = n
	}
	for range churnRequests {
		took, _ := freeMemoryGet(t, url+"/churn?mb=160")
		churn = append(churn, took)
	}
	if len(churn) == 0 {
		churn = []time.Duration{0}
	}
	slices.Sort(churn)
	// Free page reporting runs 2 s after a free; an app parks after at least
	// its idle timeout, so give the guest that long to settle.
	time.Sleep(15 * time.Second)
	_, stats := freeMemoryGet(t, url+"/stats")
	rss, private := firecrackerMemory(t, instance)

	snap := &Snapshot{
		FCVersion:         fcVersion,
		StorageKey:        "snap/" + instance + "/mem",
		VMStateStorageKey: "snap/" + instance + "/vmstate",
		VMStatePath:       filepath.Join(t.TempDir(), "vmstate"),
	}
	if _, err := m.Park(ctx, instance, SnapshotSpec{
		VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey,
	}); err != nil {
		t.Fatalf("%s: park: %v", label, err)
	}
	mem, ok, err := store.LocalPath(snap.StorageKey)
	if err != nil || !ok {
		t.Fatalf("%s: resolve memory file: ok=%v err=%v", label, ok, err)
	}
	_, allocated := fileSizes(t, mem)
	nonZero := nonZeroPages(t, mem)

	wake := func(coldCache bool) (time.Duration, time.Duration, int64, int64) {
		if coldCache {
			dropPageCache(t)
		}
		start := time.Now()
		inst, err := m.Wake(ctx, WakeRequest{
			Instance: instance, Plan: "pro", BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: memMiB, Snapshot: snap,
		})
		took := time.Since(start)
		if err != nil {
			t.Fatalf("%s: wake: %v", label, err)
		}
		if inst.Method != WakeRestore {
			t.Fatalf("%s: woke by %s, want a snapshot restore", label, inst.Method)
		}
		first, err := freeMemoryTry(fmt.Sprintf("http://%s:8080/churn?mb=64", inst.Lease.HostIP), 30*time.Second)
		if err != nil {
			_, probe := freeMemoryTry(fmt.Sprintf("http://%s:8080/", inst.Lease.HostIP), 10*time.Second)
			t.Logf("%s: first request after restore failed: %v (probe of /: %v)", label, err, probe)
			first = -1
		}
		time.Sleep(5 * time.Second)
		rss, private := firecrackerMemory(t, instance)
		if err := m.Destroy(ctx, instance); err != nil {
			t.Fatalf("%s: destroy after wake: %v", label, err)
		}
		return took, first, rss, private
	}
	for _, coldCache := range []bool{true, false} {
		var took, first []time.Duration
		var rssAfter, privateAfter []int64
		for range restores {
			w, f, r, p := wake(coldCache)
			took, first, rssAfter, privateAfter = append(took, w), append(first, f), append(rssAfter, r), append(privateAfter, p)
		}
		slices.Sort(took)
		slices.Sort(first)
		slices.Sort(rssAfter)
		slices.Sort(privateAfter)
		kind := "warm"
		if coldCache {
			kind = "cold"
		}
		t.Logf("RESULT %s restore %s: wake p50 %s min %s max %s; first churn p50 %s; host rss p50 %d MiB private %d MiB (n=%d)",
			label, kind, took[len(took)/2].Round(time.Millisecond), took[0].Round(time.Millisecond),
			took[len(took)-1].Round(time.Millisecond), first[len(first)/2].Round(time.Millisecond),
			rssAfter[len(rssAfter)/2]>>20, privateAfter[len(privateAfter)/2]>>20, restores)
	}
	t.Logf("RESULT %s capture: %d MiB allocated, %d MiB non-zero; running VM host rss %d MiB private %d MiB; churn p50 %s min %s",
		label, allocated>>20, nonZero>>20, rss>>20, private>>20, churn[len(churn)/2].Round(time.Millisecond), churn[0].Round(time.Millisecond))
	t.Logf("RESULT %s guest before capture: %s", label, strings.TrimSpace(string(stats)))
	if err := os.Remove(mem); err != nil {
		t.Logf("%s: remove memory file: %v", label, err)
	}
}

func freeMemoryGet(t *testing.T, url string) (time.Duration, []byte) {
	t.Helper()
	client := &http.Client{Timeout: 60 * time.Second}
	start := time.Now()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d err %v body %q", url, resp.StatusCode, err, body)
	}
	return time.Since(start), body
}

func freeMemoryTry(url string, timeout time.Duration) (time.Duration, error) {
	client := &http.Client{Timeout: timeout}
	start := time.Now()
	resp, err := client.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil || resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("status %d: %v", resp.StatusCode, err)
	}
	return time.Since(start), nil
}

// firecrackerMemory returns the RSS and the private (anonymous or
// copied-on-write) bytes of the instance's Firecracker process.
func firecrackerMemory(t *testing.T, instance string) (rss, private int64) {
	t.Helper()
	out, err := exec.Command("pgrep", "-f", "--", "--id "+PerInstanceScope(instance)+" ").Output()
	if err != nil {
		t.Logf("pgrep firecracker for %s: %v", instance, err)
		return 0, 0
	}
	for _, pid := range strings.Fields(string(out)) {
		comm, _ := os.ReadFile("/proc/" + pid + "/comm")
		if !strings.HasPrefix(strings.TrimSpace(string(comm)), "firecracker") {
			continue
		}
		rollup, err := os.ReadFile("/proc/" + pid + "/smaps_rollup")
		if err != nil {
			t.Logf("smaps_rollup %s: %v", pid, err)
			return 0, 0
		}
		for _, line := range strings.Split(string(rollup), "\n") {
			f := strings.Fields(line)
			if len(f) < 2 {
				continue
			}
			kb, _ := strconv.ParseInt(f[1], 10, 64)
			switch f[0] {
			case "Rss:":
				rss = kb << 10
			case "Private_Clean:", "Private_Dirty:":
				private += kb << 10
			}
		}
		return rss, private
	}
	t.Logf("no firecracker process for %s", instance)
	return 0, 0
}

// freeMemoryNodeApp serves /churn, which allocates and frees heap objects and
// buffers of mixed sizes and runs a short child process, and /stats, the
// guest's free memory by buddy order.
const freeMemoryNodeApp = `const http = require("http");
const fs = require("fs");
const cp = require("child_process");
const keep = [];
function churn(mb) {
  const live = [];
  let bytes = 0, i = 0;
  while (bytes < mb * 1048576) {
    const size = [512, 4096, 16384, 65536, 262144, 1048576][i % 6];
    if (i % 3 === 0) {
      live.push(Buffer.alloc(size, i & 255));
    } else {
      const arr = new Array(size / 64);
      for (let j = 0; j < arr.length; j++) arr[j] = { i: j, s: "v" + j };
      live.push(arr);
    }
    bytes += size;
    i++;
    if (i % 7 === 0) keep.push(live[live.length - 1]);
    if (keep.length > 40) keep.splice(0, 20);
  }
  try { cp.execFileSync(process.execPath, ["-e", "Buffer.alloc(24 << 20, 7); require('zlib').gzipSync(Buffer.alloc(8 << 20, 3))"]); } catch (e) {}
  live.length = 0;
  global.gc && global.gc();
  return bytes;
}
http.createServer((req, res) => {
  const u = new URL(req.url, "http://x");
  if (u.pathname === "/churn") {
    const mb = Number(u.searchParams.get("mb") || 64);
    res.end(JSON.stringify({ bytes: churn(mb) }));
    return;
  }
  if (u.pathname === "/stats") {
    const buddy = fs.readFileSync("/proc/buddyinfo", "utf8").trim().split("\n").map(l => l.replace(/\s+/g, " ")).join(" | ");
    const mi = fs.readFileSync("/proc/meminfo", "utf8").split("\n").filter(l => /^(MemFree|Cached|AnonPages|Slab):/.test(l)).map(l => l.replace(/\s+/g, " ")).join(", ");
    const cmd = fs.readFileSync("/proc/cmdline", "utf8").match(/(page_reporting\S*|init_on_free\S*)/g) || [];
    // virtio-balloon negotiated features: bit 4 PAGE_POISON, bit 5 REPORTING.
    let balloon = "none";
    try {
      for (const d of fs.readdirSync("/sys/bus/virtio/devices")) {
        const dev = "/sys/bus/virtio/devices/" + d;
        if (fs.readFileSync(dev + "/device", "utf8").trim() === "0x0005") {
          const f = fs.readFileSync(dev + "/features", "utf8").trim();
          balloon = "poison=" + f[4] + " reporting=" + f[5];
        }
      }
    } catch (e) { balloon = "unreadable"; }
    res.end("cmdline=" + cmd.join(",") + " ; balloon " + balloon + " ; " + mi + " ; buddy " + buddy);
    return;
  }
  res.end("ok");
}).listen(8080, "0.0.0.0");
`
