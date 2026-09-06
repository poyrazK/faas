//go:build linux && metal

package fcvm

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/frameworkready"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestMetalFrameworkReadyAcrossSnapshots(t *testing.T) {
	helper := os.Getenv("FAAS_TEST_VMMD_HELPER")
	if helper == "" || os.Getenv("FAAS_TEST_FRAMEWORK_READY") != "1" {
		t.Skip("requires isolated current guest-init and framework-ready fixture")
	}
	kernel, base, layer := metalImages(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	run := wire.ExecRunner{}
	for _, cmd := range [][]string{{"ip", "link", "add", netns.TenantBridge, "type", "bridge"}, {"ip", "addr", "add", "10.100.0.1/16", "dev", netns.TenantBridge}, {"ip", "link", "set", netns.TenantBridge, "up"}} {
		if err := run.Run(ctx, cmd); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = run.Run(context.Background(), []string{"ip", "link", "del", netns.TenantBridge}) })
	be, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	version, err := DetectFirecrackerVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	vmm := newMetalVMM(t, 30*time.Second).WithStorage(be)
	vmm.mountHelperPath = helper
	m := NewManager(run, vmm, Paths{Kernel: kernel}, version, nil, nil).WithLifecycleContext(ctx)
	m.alloc.free = []int{MaxSlots - 1, MaxSlots - 2}
	withCgroupRootAt(t, "/sys/fs/cgroup")
	received := make(chan string, 32)
	m.WithFrameworkReadyStamper(readyStamperFunc(func(_ context.Context, id string, _ time.Time) error { received <- id; return nil }))
	m.WithFrameworkReadyReader(func(ctx context.Context, id string) (frameworkready.Status, error) {
		return ReadFrameworkReady(ctx, vmm.VsockUDSSocketPath(id))
	})
	for _, id := range []string{"optprime", "opta", "optb"} {
		t.Cleanup(func() { _, _, _ = m.SignalAndKill(context.Background(), id, 0, 0) })
	}
	boot := ColdBootRequest{Instance: "optprime", BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: 128, Plan: "hobby", Runtime: "go124"}
	if _, err = m.ColdBoot(ctx, boot); err != nil {
		t.Fatal(err)
	}
	assertStatus := func(id string, want bool) {
		t.Helper()
		s, e := ReadFrameworkReady(ctx, vmm.VsockUDSSocketPath(id))
		if e != nil || s.Ready != want {
			t.Fatalf("%s readiness=%+v err=%v want=%v", id, s, e, want)
		}
	}
	assertStatus("optprime", false)
	capture := func(id, tier string) *Snapshot {
		t.Helper()
		s := &Snapshot{FCVersion: version, StorageKey: "snap/" + tier + "/mem", VMStateStorageKey: "snap/" + tier + "/vmstate", VMStatePath: filepath.Join(t.TempDir(), "state")}
		if _, e := m.Park(ctx, id, SnapshotSpec{StorageKey: s.StorageKey, VMStateStorageKey: s.VMStateStorageKey, VMStatePath: s.VMStatePath}); e != nil {
			t.Fatal(e)
		}
		return s
	}
	init := capture("optprime", "framework-init")
	wake := func(id string, s *Snapshot) *Instance {
		t.Helper()
		i, e := m.Wake(ctx, WakeRequest{Instance: id, BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: 128, Plan: "hobby", Runtime: "go124", Snapshot: s})
		if e != nil {
			t.Fatal(e)
		}
		if i.Method != WakeRestore {
			t.Fatalf("cold fallback: %s", i.RestoreError)
		}
		return i
	}
	waitReceipt := func(id string) {
		t.Helper()
		select {
		case got := <-received:
			if got != id {
				t.Fatalf("receipt identity %q want %q", got, id)
			}
		case <-time.After(4 * time.Second):
			t.Fatalf("missing receipt for %s", id)
		}
	}
	first := wake("opta", init)
	assertStatus("opta", false)
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Get("http://" + first.Lease.HostIP.String() + ":8080/signal")
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || readErr != nil {
		t.Fatalf("signal status %d body=%q err=%v", resp.StatusCode, body, readErr)
	}
	waitReceipt("opta")
	warm := capture("opta", "framework-warm")
	seen := map[string]bool{}
	for cycle := 0; cycle < 10; cycle++ {
		for _, id := range []string{"opta", "optb"} {
			i := wake(id, warm)
			assertStatus(id, true)
			waitReceipt(id)
			uuid := fetchV6UUID(t, i.Lease.HostIP.String())
			if uuid == "" || seen[uuid] {
				t.Fatalf("invalid restored UUID %q", uuid)
			}
			seen[uuid] = true
		}
		for _, id := range []string{"opta", "optb"} {
			if _, _, e := m.SignalAndKill(ctx, id, 0, 0); e != nil {
				t.Fatal(e)
			}
		}
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("live VM or lease leaked")
	}
	m.mu.Lock()
	n := len(m.frameworkReadyRuns)
	m.mu.Unlock()
	if n != 0 {
		t.Fatalf("observer leak %d", n)
	}
	t.Log("cold and init snapshot stayed pending; 20 warm restores replayed readiness with distinct UUIDs and no customer invocation")
}
