//go:build metal && linux

// adr: 192
// spec: §6.3
package fcvm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// preBootSkipRow restores once through r and returns the restore breakdown.
func preBootSkipRow(t *testing.T, r *prefetchMetalRig, capture *restoreBreakdownCapture, mutate func(*WakeRequest)) map[string]int64 {
	t.Helper()
	capture.reset()
	r.wakeWith(t, mutate)
	rows := capture.rows()
	if len(rows) != 1 {
		t.Fatalf("captured %d restore breakdowns, want 1", len(rows))
	}
	return rows[0]
}

// Restoring a capture whose drive already holds identical pre-boot files skips
// the loop mount; the files are still on every later capture, and a restore
// with changed inputs writes them again.
func TestMetalRestoreSkipsUnchangedPreBootFiles(t *testing.T) {
	r := newPrefetchMetalRig(t, "", "", 256, "", false)
	capture := newRestoreBreakdownCapture(t)
	defer capture.restore()
	ctx := context.Background()

	if row := preBootSkipRow(t, r, capture, nil); row["stage_pre_boot_files_skipped"] != 1 {
		t.Fatalf("restore of an unchanged capture did not skip the pre-boot mount: %v", row)
	}
	if _, err := r.m.Park(ctx, "prefetch-metal", r.spec); err != nil {
		t.Fatal(err)
	}
	// The capture taken after a skipped restore still carries the resolver
	// the cold boot wrote.
	drive := strings.TrimSuffix(r.memPath, "mem") + "drive"
	mnt := t.TempDir()
	if out, err := exec.Command("mount", "-o", "loop,ro", drive, mnt).CombinedOutput(); err != nil {
		t.Fatalf("mount captured drive: %v %s", err, out)
	}
	resolv, readErr := os.ReadFile(filepath.Join(mnt, serviceDiscoveryResolverPath))
	_ = exec.Command("umount", mnt).Run()
	if readErr != nil || !strings.Contains(string(resolv), "nameserver ") {
		t.Fatalf("captured drive resolver = %q, %v", resolv, readErr)
	}

	withEnv := func(req *WakeRequest) { req.APIEnvEntries = []APIEnvEntry{{Key: "CHANGED", Value: "1"}} }
	if row := preBootSkipRow(t, r, capture, withEnv); row["stage_pre_boot_files_skipped"] != 0 {
		t.Fatalf("restore with changed env skipped its pre-boot writes: %v", row)
	}
}

// A vmmd restart forgets every capture, and parks reuse the snapshot rather
// than capture again, so the capture-time record never returns. The first
// restore of the capture mounts and finds the files already on its drive;
// the next restore of the same capture — with no park or capture between —
// skips the mount.
func TestMetalRestoreLearnsReusedCaptureAfterRestart(t *testing.T) {
	r := newPrefetchMetalRig(t, "", "", 256, "", false)
	capture := newRestoreBreakdownCapture(t)
	defer capture.restore()
	ctx := context.Background()

	r.vmm.preBoot = newPreBootLedger()
	if row := preBootSkipRow(t, r, capture, nil); row["stage_pre_boot_files_skipped"] != 0 {
		t.Fatalf("first restore after a restart skipped with no record: %v", row)
	}
	if err := r.m.Destroy(ctx, "prefetch-metal"); err != nil {
		t.Fatal(err)
	}
	if row := preBootSkipRow(t, r, capture, nil); row["stage_pre_boot_files_skipped"] != 1 {
		t.Fatalf("second restore of an unchanged, reused capture did not skip: %v", row)
	}
}

// TestMetalPreBootSkipBench is the evidence run: interleaved restores of one
// unchanged app with the skip enabled and disabled. Opt-in:
//
//	FAAS_PREBOOT_SKIP_BENCH_CYCLES=12 (pairs)
func TestMetalPreBootSkipBench(t *testing.T) {
	cycles, _ := strconv.Atoi(os.Getenv("FAAS_PREBOOT_SKIP_BENCH_CYCLES"))
	if cycles <= 0 {
		t.Skip("set FAAS_PREBOOT_SKIP_BENCH_CYCLES to run the pre-boot skip A/B")
	}
	r := newPrefetchMetalRig(t, "", "", 1024, "", false)
	capture := newRestoreBreakdownCapture(t)
	defer capture.restore()
	ledger := r.vmm.preBoot
	if ledger == nil {
		t.Fatal("pre-boot ledger not wired")
	}
	arms := map[bool][]map[string]int64{}
	for i := 0; i < 2*cycles; i++ {
		on := i%2 == 1
		if !on {
			// Forget this capture's record, as a vmmd restart does: the
			// restore mounts, finds the files on the drive and re-learns the
			// capture for the next arm.
			ledger.mu.Lock()
			delete(ledger.captures, r.snap.StorageKey)
			ledger.mu.Unlock()
		}
		row := preBootSkipRow(t, r, capture, nil)
		if got := row["stage_pre_boot_files_skipped"] == 1; got != on {
			t.Fatalf("cycle %d: skipped=%v, want %v: %v", i, got, on, row)
		}
		arms[on] = append(arms[on], row)
		if _, err := r.m.Park(context.Background(), "prefetch-metal", r.spec); err != nil {
			t.Fatal(err)
		}
	}
	for _, on := range []bool{false, true} {
		for _, key := range []string{"total_ms", "stage_pre_boot_files_ms"} {
			var v []int64
			for _, row := range arms[on] {
				v = append(v, row[key])
			}
			sort.Slice(v, func(a, b int) bool { return v[a] < v[b] })
			t.Logf("skip=%-5v %-24s n=%d min=%d p50=%d p90=%d max=%d", on, key, len(v), v[0], v[len(v)/2], v[len(v)*9/10], v[len(v)-1])
		}
	}
}
