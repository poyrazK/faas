//go:build linux

package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestResourceSlotFoldsSamples(t *testing.T) {
	t0 := time.Unix(1000, 0)
	var s resourceSlot
	s.observe(t0, 100<<20, 1_000_000)
	s.observe(t0.Add(time.Second), 300<<20, 1_500_000) // 500 ms of CPU in 1 s
	s.observe(t0.Add(2*time.Second), 200<<20, 1_600_000)
	w, ok := s.take(t0.Add(5 * time.Second))
	if !ok {
		t.Fatal("slot dropped")
	}
	want := resourceSlotWire{Samples: 3, MemStartMB: 100, MemPeakMB: 300, MemEndMB: 200, CPUUsec: 600_000, CPUPeakMillicores: 500, PeakAgeMS: 4000}
	if w != want {
		t.Fatalf("slot = %+v, want %+v", w, want)
	}
	// The next slot keeps the CPU counter, so its first sample has a rate.
	s.observe(t0.Add(6*time.Second), 200<<20, 1_850_000)
	w, ok = s.take(t0.Add(6 * time.Second))
	if !ok || w.CPUUsec != 250_000 || w.MemStartMB != 200 || w.CPUPeakMillicores != 62 {
		t.Fatalf("second slot = %+v ok=%v", w, ok)
	}
}

func TestResourceSlotSkipsIdleAndEmpty(t *testing.T) {
	var s resourceSlot
	if _, ok := s.take(time.Now()); ok {
		t.Fatal("empty slot sent")
	}
	t0 := time.Unix(1000, 0)
	s.observe(t0, 50<<20, 10)
	s.observe(t0.Add(time.Second), 50<<20, 10)
	if _, ok := s.take(t0.Add(2 * time.Second)); ok {
		t.Fatal("idle slot sent")
	}
}

func TestCgroupLimitReaders(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if got := readCgroupCPULimitMillicores(write("cpu.max", "50000 100000\n")); got != 500 {
		t.Fatalf("cpu.max = %d", got)
	}
	if got := readCgroupCPULimitMillicores(write("cpu.max", "max 100000\n")); got != 0 {
		t.Fatalf("unbounded cpu.max = %d", got)
	}
	if got := readCgroupMemoryLimitMB(write("memory.max", "268435456\n")); got != 256 {
		t.Fatalf("memory.max = %d", got)
	}
	if got := readCgroupMemoryLimitMB(write("memory.max", "max\n")); got != 0 {
		t.Fatalf("unbounded memory.max = %d", got)
	}
	got, err := readCgroupCPUUsec(write("cpu.stat", "usage_usec 4242\nuser_usec 4000\nsystem_usec 242\n"))
	if err != nil || got != 4242 {
		t.Fatalf("cpu.stat = %d %v", got, err)
	}
}

// A single-process app's leaf carries no limits of its own: the VM's memory
// and vCPUs are the limit.
func TestResourceSlotLimitsFallBackToTheVM(t *testing.T) {
	leaf := t.TempDir()
	meminfo := filepath.Join(t.TempDir(), "meminfo")
	if err := os.WriteFile(meminfo, []byte("MemTotal:        1015808 kB\nMemFree:  1 kB\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := resourceSlotMeminfo
	resourceSlotMeminfo = meminfo
	t.Cleanup(func() { resourceSlotMeminfo = old })
	for name, body := range map[string]string{"memory.max": "max\n", "cpu.max": "max 100000\n"} {
		if err := os.WriteFile(filepath.Join(leaf, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mem, cpu := resourceSlotLimits(leaf)
	if mem != 992 || cpu != runtime.NumCPU()*1000 {
		t.Fatalf("limits = %d MB, %d millicores", mem, cpu)
	}
}

func TestRunResourceSlotsEmitsWithLimits(t *testing.T) {
	leaf := t.TempDir()
	for name, body := range map[string]string{
		"memory.current": "104857600\n", "memory.max": "536870912\n",
		"cpu.stat": "usage_usec 0\n", "cpu.max": "100000 100000\n",
	} {
		if err := os.WriteFile(filepath.Join(leaf, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	var cpu int64
	var got []resourceSlotWire
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runResourceSlots(ctx, leaf, 5*time.Millisecond, 20*time.Millisecond, func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			cpu += 1000
			_ = os.WriteFile(filepath.Join(leaf, "cpu.stat"), []byte("usage_usec "+strconv.FormatInt(cpu, 10)+"\n"), 0o600)
			return time.Now()
		}, func(w resourceSlotWire) error {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, w)
			if len(got) == 2 {
				cancel()
			}
			return nil
		}, slog.Default())
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("no slots emitted")
	}
	mu.Lock()
	defer mu.Unlock()
	if got[0].MemLimitMB != 512 || got[0].CPULimitMillis != 1000 || got[0].MemPeakMB != 100 {
		t.Fatalf("slot = %+v", got[0])
	}
}
