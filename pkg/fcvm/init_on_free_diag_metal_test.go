//go:build metal

package fcvm

// Diagnostic for the snapshot-size lever "zero freed guest pages": how much
// non-zero content a parked app's memory file loses when the guest kernel
// boots with init_on_free=1. Not an acceptance test; it reports numbers.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// FAAS_PROBE_APPS="node=/base.ext4:/layer.ext4,python=/base.ext4:/layer.ext4"
func probeApps(t *testing.T) map[string][2]string {
	t.Helper()
	raw := os.Getenv("FAAS_PROBE_APPS")
	if raw == "" {
		t.Skip("set FAAS_PROBE_APPS to name=base:layer pairs")
	}
	apps := map[string][2]string{}
	for _, item := range strings.Split(raw, ",") {
		name, paths, ok := strings.Cut(item, "=")
		base, layer, ok2 := strings.Cut(paths, ":")
		if !ok || !ok2 {
			t.Fatalf("bad FAAS_PROBE_APPS entry %q", item)
		}
		apps[name] = [2]string{base, layer}
	}
	return apps
}

func TestDiagnosticInitOnFreeSnapshotContent(t *testing.T) {
	kernel := os.Getenv("FAAS_TEST_KERNEL")
	if kernel == "" {
		t.Skip("set FAAS_TEST_KERNEL")
	}
	apps := probeApps(t)
	fcVersion, err := DetectFirecrackerVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	reps := 2
	for _, name := range []string{"node", "python"} {
		images, ok := apps[name]
		if !ok {
			continue
		}
		for _, variant := range []string{"default", "init_on_free"} {
			for rep := range reps {
				t.Run(fmt.Sprintf("%s/%s/%d", name, variant, rep), func(t *testing.T) {
					profile := guestTimerArgs
					if variant == "init_on_free" {
						profile += "init_on_free=1 "
					}
					saved := guestTimerProfile
					guestTimerProfile = profile
					t.Cleanup(func() { guestTimerProfile = saved })
					probeOne(t, kernel, fcVersion, name+"-"+variant, images[0], images[1])
				})
			}
		}
	}
}

func probeOne(t *testing.T, kernel, fcVersion, label, base, layer string) {
	work := t.TempDir()
	if err := os.MkdirAll(filepath.Join(work, "store", "snap"), 0o2770); err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewLocalStorageBackend(filepath.Join(work, "store"))
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(wire.ExecRunner{}, newMetalVMM(t, 60*time.Second).WithStorage(store),
		Paths{Kernel: kernel}, fcVersion, nil, nil)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	ctx := context.Background()
	instance := "iof-" + strings.ReplaceAll(label, "_", "")
	snap := &Snapshot{FCVersion: fcVersion, StorageKey: "snap/iof/mem", VMStateStorageKey: "snap/iof/vmstate", VMStatePath: filepath.Join(work, "vmstate")}
	spec := SnapshotSpec{VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey}
	const memMiB = 1024
	if _, err := m.ColdBoot(ctx, ColdBootRequest{Instance: instance, Plan: "scale", BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: memMiB}); err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })
	if _, err := m.Park(ctx, instance, spec); err != nil {
		t.Fatalf("park: %v", err)
	}
	memPath, _, err := store.LocalPath(snap.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	initContent := nonZeroBytes(t, memPath)

	inst, err := m.Wake(ctx, WakeRequest{Instance: instance, Plan: "scale", BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: memMiB, Snapshot: snap})
	if err != nil || inst.Method != WakeRestore {
		t.Fatalf("wake: method=%v err=%v", inst, err)
	}
	url := "http://" + inst.Net.HostIP.String() + ":8080/"
	client := &http.Client{Timeout: 10 * time.Second}
	start := time.Now()
	const requests = 300
	for i := range requests {
		resp, err := client.Get(url)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	traffic := time.Since(start)
	if _, err := m.Park(ctx, instance, spec); err != nil {
		t.Fatalf("park after traffic: %v", err)
	}
	afterContent := nonZeroBytes(t, memPath)
	t.Logf("PROBE %s init_content_mib=%.1f after_traffic_content_mib=%.1f requests=%d traffic_ms=%d",
		label, float64(initContent)/(1<<20), float64(afterContent)/(1<<20), requests, traffic.Milliseconds())
}

// nonZeroBytes counts the 4 KiB pages of path that hold any non-zero byte.
func nonZeroBytes(t *testing.T, path string) int64 {
	t.Helper()
	f, err := os.Open(path) //nolint:forbidigo // test-owned snapshot file.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	const page = 4096
	buf := make([]byte, 1<<20)
	zero := make([]byte, page)
	var n int64
	for {
		r, err := io.ReadFull(f, buf)
		for i := 0; i+page <= r; i += page {
			if string(buf[i:i+page]) != string(zero) {
				n += page
			}
		}
		if err != nil {
			return n
		}
	}
}
