//go:build linux && metal

package fcvm

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
	"golang.org/x/sys/unix"
)

func memoryAcceptanceMono() int64 {
	var ts unix.Timespec
	_ = unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts)
	return ts.Nano()
}

func TestMetalSnapshotMemoryHeadroom(t *testing.T) {
	out := os.Getenv("FAAS_TEST_RESTORE_ACCEPTANCE_DIR")
	if out == "" {
		t.Skip("set FAAS_TEST_RESTORE_ACCEPTANCE_DIR and current guest fixtures for memory acceptance")
	}
	ramMiB, cycles := 256, 40
	if raw := os.Getenv("FAAS_TEST_RESTORE_RAM_MB"); raw != "" {
		if _, err := fmt.Sscan(raw, &ramMiB); err != nil {
			t.Fatal(err)
		}
		if ramMiB != 128 && ramMiB != 256 && ramMiB != 512 && ramMiB != 1024 {
			t.Fatal("unsupported matrix RAM")
		}
		cycles = 4
	}
	kernel, base, layer := metalImages(t)
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	run := wire.ExecRunner{}
	for _, argv := range [][]string{{"ip", "link", "add", netns.TenantBridge, "type", "bridge"}, {"ip", "addr", "add", "10.100.0.1/16", "dev", netns.TenantBridge}, {"ip", "link", "set", netns.TenantBridge, "up"}} {
		if err := run.Run(ctx, argv); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = run.Run(context.Background(), []string{"ip", "link", "del", netns.TenantBridge}) })
	store, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ver, err := DetectFirecrackerVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	vmm := NewJailerVMM(JailChrootBase, 30*time.Second).WithStorage(store)
	vmm.mountHelperPath = os.Getenv("FAAS_TEST_VMMD_HELPER")
	m := NewManager(run, vmm, Paths{Kernel: kernel}, ver, slog.Default(), nil)
	m.alloc.free = []int{MaxSlots - 1, MaxSlots - 2, MaxSlots - 3, MaxSlots - 4, MaxSlots - 5, MaxSlots - 6}
	if err := m.EnablePreparedNetworks(ctx, 3); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := m.ClosePreparedNetworks(); err != nil {
			t.Error(err)
		}
	}()
	withCgroupRootAt(t, "/sys/fs/cgroup")
	names := []string{"opta", "optb", "optc"}
	for _, name := range append(names, "optprime") {
		t.Cleanup(func() { _, _, _ = m.SignalAndKill(context.Background(), name, 0, 0) })
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, Timeout: 5 * time.Second}
	request := func(inst *Instance) (int64, int64, string, error) {
		start := memoryAcceptanceMono()
		r, err := client.Get("http://" + inst.Lease.HostIP.String() + ":8080/")
		head := memoryAcceptanceMono()
		if err != nil {
			return start, head, "", err
		}
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return start, head, "", err
		}
		var obj struct {
			OK   bool   `json:"ok"`
			UUID string `json:"uuid"`
		}
		if err = json.Unmarshal(body, &obj); err != nil {
			return start, head, "", fmt.Errorf("decode fixture response: %w", err)
		}
		if r.StatusCode != 200 || !obj.OK {
			return start, head, "", fmt.Errorf("fixture response status=%d body=%s", r.StatusCode, body)
		}
		return start, head, obj.UUID, nil
	}
	prime, err := m.ColdBoot(ctx, ColdBootRequest{Instance: "optprime", BaseKey: base, LayerKey: layer, VcpuCount: 4, MemSizeMiB: ramMiB, Plan: "scale", EgressMbit: api.MustLimitsFor("scale").EgressMbit})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := request(prime); err != nil {
		t.Fatal(err)
	}
	snap := &Snapshot{FCVersion: ver, StorageKey: "snap/profile/mem", VMStateStorageKey: "snap/profile/vmstate", VMStatePath: filepath.Join(t.TempDir(), "vmstate")}
	if _, err := m.Park(ctx, "optprime", SnapshotSpec{StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey, VMStatePath: snap.VMStatePath}); err != nil {
		t.Fatal(err)
	}
	memPath, _, err := store.LocalPath(snap.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	sparsePath := filepath.Join(out, "prepared-sparse.mem")
	prepStart := time.Now()
	if err := exec.CommandContext(ctx, "cp", "--reflink=never", "--sparse=always", memPath, sparsePath).Run(); err != nil {
		t.Fatal(err)
	}
	prepMS := float64(time.Since(prepStart)) / float64(time.Millisecond)
	digest := func(path string) string {
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("%x", h.Sum(nil))
	}
	if digest(memPath) != digest(sparsePath) {
		t.Fatal("sparse preparation changed snapshot bytes")
	}
	var stat unix.Stat_t
	if err := unix.Stat(sparsePath, &stat); err != nil {
		t.Fatal(err)
	}
	t.Logf("prepared sparse snapshot: bytes=%d allocated_bytes=%d sha256=%s prepare_ms=%f", stat.Size, stat.Blocks*512, digest(sparsePath), prepMS)
	densePath := filepath.Join(out, "prepared-dense.mem")
	if err := exec.CommandContext(ctx, "cp", "--reflink=never", "--sparse=never", sparsePath, densePath).Run(); err != nil {
		t.Fatal(err)
	}
	if digest(densePath) != digest(sparsePath) {
		t.Fatal("dense input differs")
	}
	if err := unix.Stat(densePath, &stat); err != nil {
		t.Fatal(err)
	}
	if stat.Blocks*512 < stat.Size {
		t.Fatal("dense input still has holes")
	}
	t.Logf("dense snapshot bytes=%d allocated_bytes=%d", stat.Size, stat.Blocks*512)
	type backing struct{ name, path, binDir string }

	sparse := os.Getenv("FAAS_TEST_RESTORE_RUNTIME_DIR")
	if sparse == "" {
		t.Fatal("set FAAS_TEST_RESTORE_RUNTIME_DIR to the candidate runtime directory")
	}
	backings := []backing{{"sparse_input", sparsePath, sparse}, {"dense_input", densePath, sparse}}
	var mu sync.Mutex
	var rows []map[string]any
	seen := map[string]bool{}
	liveUUIDs := map[string]string{}
	for cycle := 0; cycle < cycles; cycle++ {
		variant := backings[cycle%len(backings)]
		names = []string{fmt.Sprintf("optanon-%d-a", cycle), fmt.Sprintf("optanon-%d-b", cycle), fmt.Sprintf("optanon-%d-c", cycle)}
		for _, name := range names {
			t.Cleanup(func() { _, _, _ = m.SignalAndKill(context.Background(), name, 0, 0) })
		}

		t.Setenv("PATH", variant.binDir+":/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
		policy, _ := m.preparedPolicy(WakeRequest{Plan: "scale", EgressMbit: api.MustLimitsFor("scale").EgressMbit})
		m.preparedNetworks.observe(policy)
		limit := time.Now().Add(5 * time.Second)
		for {
			m.preparedNetworks.mu.Lock()
			n := len(m.preparedNetworks.ready)
			m.preparedNetworks.mu.Unlock()
			if n == 3 {
				break
			}
			if time.Now().After(limit) {
				t.Fatal("cache not ready")
			}
			time.Sleep(time.Millisecond)
		}

		selected := *snap
		selected.StorageKey = variant.path
		var wg sync.WaitGroup
		for _, name := range names {
			wg.Add(1)
			go func() {
				defer wg.Done()
				start := memoryAcceptanceMono()
				inst, err := m.Wake(ctx, WakeRequest{Instance: name, BaseKey: base, LayerKey: layer, VcpuCount: 4, MemSizeMiB: ramMiB, Plan: "scale", EgressMbit: api.MustLimitsFor("scale").EgressMbit, Snapshot: &selected})
				wake := memoryAcceptanceMono()
				if err != nil {
					mu.Lock()
					rows = append(rows, map[string]any{"cycle": cycle, "backing": variant.name, "instance": name, "error": err.Error(), "elapsed_ms": float64(memoryAcceptanceMono()-start) / 1e6})
					mu.Unlock()
					t.Error(err)
					return
				}
				pid, _ := m.InstancePID(name)
				req, head, id, err := request(inst)
				done := memoryAcceptanceMono()
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					t.Error(err)
				}
				if inst.Method != WakeRestore {
					t.Errorf("fallback: %s", inst.RestoreError)
				}
				if id == "" || seen[id] {
					t.Errorf("missing/duplicate UUID %q", id)
				}
				seen[id] = true
				liveUUIDs[name] = id
				rows = append(rows, map[string]any{"cycle": cycle, "backing": variant.name, "method": inst.Method.String(), "restore_error": inst.RestoreError, "full_response_ms": float64(done-start) / 1e6, "instance": name, "pid": pid, "start_ns": start, "wake_ns": wake, "request_ns": req, "headers_ns": head, "wake_ms": float64(wake-start) / 1e6, "guest_response_ms": float64(head-req) / 1e6, "wake_and_response_ms": float64(head-start) / 1e6, "uuid": id})
			}()
		}
		wg.Wait()
		if !t.Failed() {
			time.Sleep(time.Second)
		}

		{
			for _, name := range names {
				pid, ok := m.InstancePID(name)
				if !ok {
					t.Errorf("guest %s exited during post-response hold", name)
					continue
				}
				if ok {
					cg := filepath.Join(cgroupRoot, ParentCgroupFor("scale"), PerInstanceScope(name))
					fences := map[string]string{}
					for _, file := range []string{"memory.current", "memory.peak", "memory.events", "memory.max", "cpu.max"} {
						if v, err := os.ReadFile(filepath.Join(cg, file)); err == nil {
							fences[file] = string(v)
						}
					}
					for _, line := range strings.Split(strings.TrimSpace(fences["memory.events"]), "\n") {
						fields := strings.Fields(line)
						if len(fields) != 2 {
							t.Errorf("missing/malformed memory event %q", line)
							continue
						}
						if fields[0] == "max" || fields[0] == "oom" || fields[0] == "oom_kill" {
							if fields[1] != "0" {
								t.Errorf("guest %s exceeded memory headroom: %s", name, line)
							}
						}
					}
					wantFence := strconv.FormatInt(int64(ramMiB+8)<<20, 10)
					if strings.TrimSpace(fences["memory.max"]) != wantFence {
						t.Errorf("memory fence changed: %q want %s", fences["memory.max"], wantFence)
					}
					if inst := m.LiveInstances()[name]; inst == nil {
						t.Errorf("missing live instance %s", name)
					} else {
						_, _, id, err := request(inst)
						if err != nil || id != liveUUIDs[name] {
							t.Errorf("post-hold request for %s: id=%s expected=%s error=%v", name, id, liveUUIDs[name], err)
						}
					}
					encoded, _ := json.MarshalIndent(fences, "", "  ")
					_ = os.WriteFile(filepath.Join(out, fmt.Sprintf("cgroup-%s-%s.json", variant.name, name)), encoded, 0600)
					data, err := os.ReadFile(fmt.Sprintf("/proc/%d/smaps", pid))
					if err == nil {
						_ = os.WriteFile(filepath.Join(out, fmt.Sprintf("smaps-%s-%s.txt", variant.name, name)), data, 0600)
					}
				}
			}
		}
		for _, name := range names {
			if _, _, err := m.SignalAndKill(ctx, name, 0, 0); err != nil {
				t.Fatal(err)
			}
		}
		if t.Failed() {
			break
		}
	}
	data, _ := json.MarshalIndent(rows, "", "  ")
	if err := os.WriteFile(filepath.Join(out, "samples.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.ClosePreparedNetworks(); err != nil {
		t.Fatal(err)
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 || len(m.alloc.reserved) != 0 {
		t.Fatal("leaked live instance or lease")
	}
}
