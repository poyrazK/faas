//go:build metal

// adr: 637

package fcvm

// Diagnostic for ADR-637 (production-us hunt #5, H5-25): restored guests on
// Firecracker 1.7 lost timer interrupts, so a 1 s timer fired every 5 s. The
// guest runs a 250 ms tick loop; the test cold-boots it, lets it run, parks it
// and restores the capture several times, then counts how often the guest
// ticked after each restore. FAAS_DIAG_GUEST_TIMER=legacy boots the pre-ADR-637
// tsc/lapic-deadline profile as the control.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

const timerDiagTick = "( n=0; while :; do n=$((n+1)); echo $n > /var/tmp/ticks; /bin/busybox usleep 250000; done ) &\n" +
	"cat /sys/devices/system/clocksource/clocksource0/current_clocksource /sys/devices/system/clockevents/clockevent0/current_device > /var/tmp/clock 2>&1\n"

func timerDiagGet(ip, path string) (string, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + ip + ":8080" + path)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return strings.TrimSpace(string(body)), err
}

// timerDiagSample polls the guest's tick counter every 100 ms for d and
// returns the ticks observed and the longest wall-clock stretch without one.
func timerDiagSample(ip string, d time.Duration) (ticks int, longest time.Duration) {
	start, last, lastAt := time.Now(), -1, time.Now()
	for time.Since(start) < d {
		if body, err := timerDiagGet(ip, "/var/tmp/ticks"); err == nil {
			if n, err := strconv.Atoi(body); err == nil && n != last {
				if last >= 0 {
					ticks += n - last
				}
				last, lastAt = n, time.Now()
			}
		}
		if gap := time.Since(lastAt); gap > longest {
			longest = gap
		}
		time.Sleep(100 * time.Millisecond)
	}
	return ticks, longest
}

func TestDiagnosticGuestTimersAfterRestore(t *testing.T) {
	if os.Getenv("FAAS_DIAG_GUEST_TIMER") == "" {
		t.Skip("set FAAS_DIAG_GUEST_TIMER=new|legacy to run the ADR-637 timer diagnostic")
	}
	if os.Getenv("FAAS_DIAG_GUEST_TIMER") == "legacy" {
		previous := guestTimerProfile
		guestTimerProfile = ""
		t.Cleanup(func() { guestTimerProfile = previous })
	}
	kernel, _, _ := metalImages(t)
	runFor := 90 * time.Second
	if v, err := strconv.Atoi(os.Getenv("FAAS_DIAG_TIMER_RUN_SECONDS")); err == nil && v > 0 {
		runFor = time.Duration(v) * time.Second
	}
	restores := 5
	if v, err := strconv.Atoi(os.Getenv("FAAS_DIAG_TIMER_RESTORES")); err == nil && v > 0 {
		restores = v
	}

	dir := t.TempDir()
	v6ShimExtra = timerDiagTick
	t.Cleanup(func() { v6ShimExtra = "" })
	base, layer := filepath.Join(dir, "timer-base.ext4"), filepath.Join(dir, "timer-layer.ext4")
	if err := buildV6BaseExt4Port(base, "", 8080); err != nil {
		t.Fatal(err)
	}
	if err := buildV6LayerExt4(layer); err != nil {
		t.Fatal(err)
	}
	snapshotRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(snapshotRoot, "snap"), 0o2770); err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewLocalStorageBackend(snapshotRoot)
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(wire.ExecRunner{}, newMetalVMM(t, 60*time.Second).WithStorage(store),
		Paths{Kernel: kernel}, os.Getenv("FAAS_TEST_FC_VERSION"), nil, nil)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	ctx, cancel := context.WithTimeout(context.Background(), runFor+time.Duration(restores)*time.Minute+5*time.Minute)
	defer cancel()

	coldStart := time.Now()
	prime, err := m.ColdBoot(ctx, ColdBootRequest{Instance: "timer-prime", Plan: "hobby", BaseKey: base, LayerKey: layer, VcpuCount: 4, MemSizeMiB: 256})
	if err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	coldBoot := time.Since(coldStart)
	names := []string{"timer-prime"}
	t.Cleanup(func() {
		for _, name := range names {
			_ = m.Destroy(context.Background(), name)
		}
	})
	ip := prime.Lease.HostIP.String()
	clock, _ := timerDiagGet(ip, "/var/tmp/clock")
	t.Logf("profile=%q cold_boot=%s clock=%q", guestTimerProfile, coldBoot.Round(time.Millisecond), clock)
	ticks, longest := timerDiagSample(ip, 5*time.Second)
	t.Logf("cold: ticks=%d in 5s longest_gap=%s", ticks, longest.Round(time.Millisecond))
	time.Sleep(runFor)

	snap := &Snapshot{FCVersion: os.Getenv("FAAS_TEST_FC_VERSION"), StorageKey: "snap/timer/mem",
		VMStateStorageKey: "snap/timer/vmstate", VMStatePath: filepath.Join(t.TempDir(), "vmstate")}
	if _, err := m.Park(ctx, "timer-prime", SnapshotSpec{VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey}); err != nil {
		t.Fatalf("park: %v", err)
	}
	stalled := 0
	for i := 0; i < restores; i++ {
		name := fmt.Sprintf("timer-r%d", i)
		names = append(names, name)
		inst, err := m.Wake(ctx, WakeRequest{Instance: name, Plan: "hobby", BaseKey: base, LayerKey: layer, VcpuCount: 4, MemSizeMiB: 256, Snapshot: snap})
		if err != nil {
			t.Fatalf("restore %d: %v", i, err)
		}
		if inst.Method != WakeRestore {
			t.Fatalf("restore %d came up via %s", i, inst.Method)
		}
		ticks, longest := timerDiagSample(inst.Lease.HostIP.String(), 8*time.Second)
		if longest > 1500*time.Millisecond || ticks < 20 {
			stalled++
		}
		t.Logf("restore %d: ticks=%d in 8s (want ~32) longest_gap=%s", i, ticks, longest.Round(time.Millisecond))
		if err := m.Destroy(ctx, name); err != nil {
			t.Fatalf("destroy %s: %v", name, err)
		}
	}
	t.Logf("SUMMARY profile=%q stalled=%d/%d cold_boot=%s", guestTimerProfile, stalled, restores, coldBoot.Round(time.Millisecond))
	if os.Getenv("FAAS_DIAG_GUEST_TIMER") != "legacy" && stalled > 0 {
		t.Fatalf("%d of %d restores stalled guest timers with the ADR-637 profile", stalled, restores)
	}
}
