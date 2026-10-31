package vmmdgrpc

// adr: 20261030100950 — vmmd reports the tenant slice's CPU pressure and the
// host's busy share of its CPUs over a fixed window.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestParsePressureSomeAvg10(t *testing.T) {
	for text, want := range map[string]float64{
		"some avg10=12.50 avg60=3.00 avg300=1.00 total=99\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n": 12.5,
		"full avg10=7.00 avg60=0 avg300=0 total=1\nsome avg10=0.00 avg60=0 avg300=0 total=1\n":               0,
		"some avg60=3.00 total=1\n": -1,
		"some avg10=x total=1\n":    -1,
		"":                          -1,
	} {
		got, ok := parsePressureSomeAvg10(text)
		if (want < 0 && ok) || (want >= 0 && (!ok || got != want)) {
			t.Errorf("parse %q = %v %v, want %v", text, got, ok, want)
		}
	}
}

// fakeProcStat points procStatPath at a file whose aggregate cpu line holds
// busy and idle ticks.
func fakeProcStat(t *testing.T) func(busy, idle uint64) {
	t.Helper()
	saved := procStatPath
	t.Cleanup(func() { procStatPath = saved })
	procStatPath = filepath.Join(t.TempDir(), "stat")
	return func(busy, idle uint64) {
		// user carries the busy ticks; guest repeats them, as the kernel
		// counts guest time in user too, and must not be counted twice.
		line := fmt.Sprintf("cpu  %d 0 0 %d 0 0 0 0 %d 0\ncpu0 1 2 3 4 5 6 7 8 9 10\n", busy, idle, busy)
		if err := os.WriteFile(procStatPath, []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHostCPUBusyPercent(t *testing.T) {
	set := fakeProcStat(t)
	var h hostCPUSampler
	start := time.Unix(1_800_000_000, 0)
	window := time.Duration(api.NodeCPUBusyWindowSeconds) * time.Second

	set(1000, 1000)
	if _, ok := h.busyPercent(start); ok {
		t.Fatal("a single reading reported a busy share")
	}
	// Under half the window: not yet reported.
	set(1300, 1100)
	if _, ok := h.busyPercent(start.Add(window/2 - time.Second)); ok {
		t.Fatal("reported before the readings spanned half the window")
	}
	// 9 of 10 ticks busy since the first reading.
	set(1900, 1100)
	if got, ok := h.busyPercent(start.Add(window / 2)); !ok || got != 90 {
		t.Fatalf("busy = %v %v, want 90", got, ok)
	}
	// Readings older than the window age out: only the last stretch counts.
	at := start.Add(window / 2)
	busy, idle := uint64(1900), uint64(1100)
	for range 2 * api.NodeCPUBusyWindowSeconds {
		at = at.Add(time.Second)
		busy, idle = busy+10, idle+90
		set(busy, idle)
		h.busyPercent(at)
	}
	if got, ok := h.busyPercent(at); !ok || got != 10 {
		t.Fatalf("busy after the window moved on = %v %v, want 10", got, ok)
	}
	if len(h.samples) > api.NodeCPUBusyWindowSeconds+1 {
		t.Fatalf("kept %d readings, want at most one a second over the window", len(h.samples))
	}

	procStatPath = filepath.Join(t.TempDir(), "missing")
	if _, ok := h.busyPercent(at.Add(time.Second)); ok {
		t.Fatal("reported a busy share without /proc/stat")
	}
}

func TestStatsCarriesCPUPressureReadings(t *testing.T) {
	set := fakeProcStat(t)
	saved := tenantCPUPressurePath
	t.Cleanup(func() { tenantCPUPressurePath = saved })
	tenantCPUPressurePath = filepath.Join(t.TempDir(), "cpu.pressure")
	s := &Server{}
	start := time.Unix(1_800_000_000, 0)

	resp := &vmmdpb.StatsResponse{}
	s.setCPUPressureReadings(resp, start)
	if resp.GetTenantCpuPressureSomeAvg10() != nil || resp.GetHostCpuBusyPercent() != nil {
		t.Fatalf("readings without the files: %v", resp)
	}

	if err := os.WriteFile(tenantCPUPressurePath, []byte("some avg10=33.00 avg60=1 avg300=1 total=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	set(0, 0)
	s.setCPUPressureReadings(&vmmdpb.StatsResponse{}, start)
	set(950, 50)
	resp = &vmmdpb.StatsResponse{}
	s.setCPUPressureReadings(resp, start.Add(time.Duration(api.NodeCPUBusyWindowSeconds)*time.Second))
	if v := resp.GetTenantCpuPressureSomeAvg10(); v == nil || v.GetValue() != 33 {
		t.Fatalf("pressure = %v, want 33", v)
	}
	if v := resp.GetHostCpuBusyPercent(); v == nil || v.GetValue() != 95 {
		t.Fatalf("busy = %v, want 95", v)
	}
}
