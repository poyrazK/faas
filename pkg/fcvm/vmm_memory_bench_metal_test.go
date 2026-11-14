//go:build metal

// adr: 20261101215228

package fcvm

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestMetalVMMMemoryBench measures what each instance's Firecracker process
// holds on the host apart from the guest's RAM: its heap, thread stacks and
// device buffers, after a cold boot and again after a restore of the same
// instance. The guest memory mapping is reported separately; plan RAM already
// pays for it.
//
// Opt-in: FAAS_VMM_MEMORY_BENCH_VMS=N instances; unset, it skips.
// FAAS_VMM_MEMORY_BENCH_MEM_MIB (default 256) is each guest's RAM.
func TestMetalVMMMemoryBench(t *testing.T) {
	vms, _ := strconv.Atoi(os.Getenv("FAAS_VMM_MEMORY_BENCH_VMS"))
	if vms <= 0 {
		t.Skip("set FAAS_VMM_MEMORY_BENCH_VMS to run the VMM memory bench")
	}
	memMiB := 256
	if v, err := strconv.Atoi(os.Getenv("FAAS_VMM_MEMORY_BENCH_MEM_MIB")); err == nil && v >= 64 {
		memMiB = v
	}
	kernel, base, layer := metalImages(t)
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
	v := newMetalVMM(t, 30*time.Second).WithStorage(store)
	m := NewManager(wire.ExecRunner{}, v, Paths{Kernel: kernel}, fcVersion, nil, nil)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	names := make([]string, vms)
	snaps := make([]*Snapshot, vms)
	stateDir := t.TempDir()
	for i := range names {
		names[i] = fmt.Sprintf("vmmmem-%d", i)
		name := names[i]
		t.Cleanup(func() { _ = m.Destroy(context.Background(), name) })
		snaps[i] = &Snapshot{
			FCVersion: fcVersion, StorageKey: "snap/" + name + "/mem", VMStateStorageKey: "snap/" + name + "/vmstate",
			VMStatePath: filepath.Join(stateDir, name+".vmstate"),
		}
		if _, err := m.ColdBoot(ctx, ColdBootRequest{Instance: name, Plan: "pro", BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: memMiB}); err != nil {
			t.Fatalf("cold boot %s: %v", name, err)
		}
	}
	time.Sleep(3 * time.Second)
	logVMMMemory(t, "cold boot", v, names, memMiB)

	for i, name := range names {
		spec := SnapshotSpec{VMStatePath: snaps[i].VMStatePath, StorageKey: snaps[i].StorageKey, VMStateStorageKey: snaps[i].VMStateStorageKey}
		if _, err := m.Park(ctx, name, spec); err != nil {
			t.Fatalf("park %s: %v", name, err)
		}
	}
	for i, name := range names {
		out, err := m.Wake(ctx, WakeRequest{Instance: name, Plan: "pro", BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: memMiB, Snapshot: snaps[i]})
		if err != nil {
			t.Fatalf("wake %s: %v", name, err)
		}
		if out.Method != WakeRestore {
			t.Fatalf("wake %s = %s, want a restore", name, out.Method)
		}
	}
	time.Sleep(3 * time.Second)
	logVMMMemory(t, "restore", v, names, memMiB)

	for _, name := range names {
		if err := m.Destroy(ctx, name); err != nil {
			t.Fatalf("destroy %s: %v", name, err)
		}
	}
	leakcheck.AssertZero(t)
}

// vmmMemory splits one Firecracker process's memory into the guest RAM
// mapping and everything else.
type vmmMemory struct {
	GuestRSSKB   int // resident pages of the guest memory mapping
	OtherRSSKB   int // resident pages outside it
	OtherPssKB   int // the same, shared pages split between processes
	OtherPrivKB  int // pages outside the guest mapping only this process holds
	OtherAnonKB  int // anonymous pages outside the guest mapping
	Threads      int
	GuestMapping int // mappings counted as guest memory
}

func logVMMMemory(t *testing.T, phase string, v *JailerVMM, names []string, memMiB int) {
	t.Helper()
	var sum vmmMemory
	for _, name := range names {
		pid, ok := v.InstancePID(name)
		if !ok {
			t.Fatalf("%s: no firecracker pid for %s", phase, name)
		}
		mem, err := readVMMMemory(pid, memMiB*1024)
		if err != nil {
			t.Fatalf("%s: %s: %v", phase, name, err)
		}
		if mem.GuestMapping == 0 {
			t.Fatalf("%s: %s: no mapping of the guest's %d MiB found", phase, name, memMiB)
		}
		t.Logf("%-9s %-9s guest rss %6.1f MB | other rss %5.1f MB, pss %5.1f MB, private %5.1f MB, anon %5.1f MB | threads %d",
			phase, name, kbMB(mem.GuestRSSKB), kbMB(mem.OtherRSSKB), kbMB(mem.OtherPssKB), kbMB(mem.OtherPrivKB), kbMB(mem.OtherAnonKB), mem.Threads)
		sum.GuestRSSKB += mem.GuestRSSKB
		sum.OtherRSSKB += mem.OtherRSSKB
		sum.OtherPssKB += mem.OtherPssKB
		sum.OtherPrivKB += mem.OtherPrivKB
		sum.OtherAnonKB += mem.OtherAnonKB
		sum.Threads += mem.Threads
	}
	n := len(names)
	t.Logf("%-9s mean over %d: guest rss %.1f MB | other rss %.1f MB, pss %.1f MB, private %.1f MB, anon %.1f MB | threads %.1f",
		phase, n, kbMB(sum.GuestRSSKB/n), kbMB(sum.OtherRSSKB/n), kbMB(sum.OtherPssKB/n), kbMB(sum.OtherPrivKB/n), kbMB(sum.OtherAnonKB/n), float64(sum.Threads)/float64(n))
}

// readVMMMemory reads /proc/<pid>/smaps. A mapping at least as large as the
// guest's RAM is guest memory: Firecracker maps guest RAM as one region below
// 3 GiB, anonymous after a cold boot and the snapshot file after a restore.
func readVMMMemory(pid, guestKB int) (vmmMemory, error) {
	var mem vmmMemory
	f, err := os.Open(fmt.Sprintf("/proc/%d/smaps", pid))
	if err != nil {
		return mem, err
	}
	defer func() { _ = f.Close() }()
	type mapping struct{ size, rss, pss, private, anon int }
	var all []mapping
	var cur *mapping
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 {
			continue
		}
		if !strings.HasSuffix(fields[0], ":") { // a mapping header: "start-end perms ..."
			all = append(all, mapping{})
			cur = &all[len(all)-1]
			continue
		}
		if cur == nil || len(fields) < 2 {
			continue
		}
		v, _ := strconv.Atoi(fields[1])
		switch fields[0] {
		case "Size:":
			cur.size = v
		case "Rss:":
			cur.rss = v
		case "Pss:":
			cur.pss = v
		case "Private_Clean:", "Private_Dirty:":
			cur.private += v
		case "Anonymous:":
			cur.anon = v
		}
	}
	if err := sc.Err(); err != nil {
		return mem, err
	}
	for _, mp := range all {
		if mp.size >= guestKB {
			mem.GuestMapping++
			mem.GuestRSSKB += mp.rss
			continue
		}
		mem.OtherRSSKB += mp.rss
		mem.OtherPssKB += mp.pss
		mem.OtherPrivKB += mp.private
		mem.OtherAnonKB += mp.anon
	}
	if tasks, err := os.ReadDir(fmt.Sprintf("/proc/%d/task", pid)); err == nil {
		mem.Threads = len(tasks)
	}
	return mem, nil
}

func kbMB(kb int) float64 { return float64(kb) / 1024 }
