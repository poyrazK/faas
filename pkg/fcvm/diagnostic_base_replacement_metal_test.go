//go:build metal && linux

package fcvm

// spec: §4.6 — diagnostic: restore a snapshot after its read-only base drive
// was replaced, as a runtime-base cache refresh does between releases.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// baseReplacementResult is the durable diagnostic record.
type baseReplacementResult struct {
	Replaced         bool                `json:"base_replaced"`
	FreshManager     bool                `json:"fresh_manager"`
	BaseInodeCapture uint64              `json:"base_inode_at_capture"`
	BaseInodeRestore uint64              `json:"base_inode_at_restore"`
	Method           string              `json:"restore_method"`
	WakeError        string              `json:"wake_error,omitempty"`
	Statuses         map[string]int      `json:"statuses"`
	Digests          map[int][]string    `json:"digests_by_task"`
	Inconsistent     []int               `json:"tasks_with_more_than_one_digest"`
	PIDs             map[int]int         `json:"responses_by_pid"`
	RuntimeErrors    []string            `json:"runtime_errors"`
	Lifecycle        []map[string]string `json:"lifecycle"`
}

var baseReplacementErrorMarkers = []string{
	"Check failed", "Fatal error", "Segmentation fault", "SIGSEGV", "SIGILL", "SIGBUS",
	"Illegal instruction", "Bus error", "handler error", "core dumped", "Aborted",
}

// TestDiagnosticRestoreAfterBaseReplacement captures an init snapshot of a
// Node function on drive0 image A, optionally replaces the drive0 file with
// image B (same path, new inode — what ensureBaseGeneration's cache refresh
// does between releases), restores, and drives invocations that fault in
// node binary pages the process had not touched before capture.
//
//	FAAS_TEST_BASE_ROOTFS        image A (the capture-time base)
//	FAAS_DIAGNOSTIC_BASE_B       image B; empty runs the same-base control
//	FAAS_DIAGNOSTIC_FRESH_MANAGER=1  restore through a new Manager (vmmd restart)
//	FAAS_DIAGNOSTIC_RESULT_PATH  absolute path for the JSON record
func TestDiagnosticRestoreAfterBaseReplacement(t *testing.T) {
	output := os.Getenv("FAAS_DIAGNOSTIC_RESULT_PATH")
	if !filepath.IsAbs(output) {
		t.Skip("set FAAS_DIAGNOSTIC_RESULT_PATH to run the base-replacement diagnostic")
	}
	kernel, baseA, layer := metalImages(t)
	baseB := os.Getenv("FAAS_DIAGNOSTIC_BASE_B")
	fresh := os.Getenv("FAAS_DIAGNOSTIC_FRESH_MANAGER") == "1"
	withCgroupRootAt(t, "/sys/fs/cgroup")

	dir := benchFixtureDir(t)
	drive0 := filepath.Join(dir, "drive0.ext4")
	copyFileForDiagnostic(t, baseA, drive0)

	storeRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(storeRoot, "snap"), 0o2770); err != nil {
		t.Fatal(err)
	}
	backend, err := storage.NewLocalStorageBackend(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	version, err := DetectFirecrackerVersion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	vmm := newMetalVMM(t, 30*time.Second).WithStorage(backend)
	stageBenchMountHelper(t, vmm)
	m := NewManager(wire.ExecRunner{}, vmm, Paths{Kernel: kernel}, version, nil, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Minute)
	defer cancel()

	const name = "diag-base-replacement"
	res := &baseReplacementResult{Replaced: baseB != "", FreshManager: fresh,
		Statuses: map[string]int{}, Digests: map[int][]string{}, PIDs: map[int]int{}}
	var mu sync.Mutex
	save := func() {
		mu.Lock()
		defer mu.Unlock()
		data, _ := json.MarshalIndent(res, "", "  ")
		_ = os.WriteFile(output+".tmp", data, 0o600)
		_ = os.Rename(output+".tmp", output)
	}
	record := func(stage string) {
		mu.Lock()
		res.Lifecycle = append(res.Lifecycle, map[string]string{"stage": stage, "at_utc": time.Now().UTC().Format(time.RFC3339Nano)})
		mu.Unlock()
		save()
	}
	active := m
	t.Cleanup(func() {
		save()
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		_ = active.Destroy(cctx, name)
		if active != m {
			_ = m.Destroy(cctx, name)
		}
	})

	req := ColdBootRequest{Instance: name, Plan: "scale", HealthcheckPath: "/healthz", BaseKey: drive0, LayerKey: layer,
		VcpuCount: 4, MemSizeMiB: 1024, CPUMillicores: 1000}
	if _, err := m.ColdBoot(ctx, req); err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	record("cold_ready")
	key := state.SnapshotCaptureMemKey(name, state.SnapshotTierInit, "base-replacement")
	snap := &Snapshot{FCVersion: version, StorageKey: key,
		VMStateStorageKey: state.SnapshotVMStateKey(state.Snapshot{StorageKey: key}),
		VMStatePath:       filepath.Join(t.TempDir(), "vmstate")}
	res.BaseInodeCapture = inodeOf(t, drive0)
	if _, err := m.Park(ctx, name, SnapshotSpec{VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey}); err != nil {
		t.Fatalf("init capture: %v", err)
	}
	record("init_captured_and_parked")

	if baseB != "" {
		// Same path, new inode: the cache refresh writes a temp file and
		// renames it over the cached base.
		copyFileForDiagnostic(t, baseB, drive0+".refresh")
		if err := os.Rename(drive0+".refresh", drive0); err != nil {
			t.Fatal(err)
		}
		record("drive0_replaced")
	}
	res.BaseInodeRestore = inodeOf(t, drive0)
	if fresh {
		active = NewManager(wire.ExecRunner{}, vmm, Paths{Kernel: kernel}, version, nil, nil)
	}

	inst, err := active.Wake(ctx, WakeRequest{Instance: name, Plan: "scale", HealthcheckPath: "/healthz", BaseKey: drive0, LayerKey: layer,
		VcpuCount: 4, MemSizeMiB: 1024, CPUMillicores: 1000, Snapshot: snap})
	if err != nil {
		res.WakeError = err.Error()
		record("wake_failed")
		return
	}
	res.Method = inst.Method.String()
	record("woken")

	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ring := active.LogRing(name)
		var cursor int64 = 1
		read := func() {
			if ring == nil {
				return
			}
			for _, line := range ring.Snapshot(cursor) {
				cursor = line.Seq + 1
				for _, marker := range baseReplacementErrorMarkers {
					if strings.Contains(line.Line, marker) {
						mu.Lock()
						if len(res.RuntimeErrors) < 64 {
							res.RuntimeErrors = append(res.RuntimeErrors, line.Line)
						}
						mu.Unlock()
						break
					}
				}
			}
		}
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				read()
				return
			case <-ticker.C:
				read()
			}
		}
	}()

	client := &http.Client{Timeout: 10 * time.Second}
	slots := make(chan struct{}, 8)
	var pending sync.WaitGroup
	for i := 0; i < 960; i++ {
		mu.Lock()
		broken := res.Statuses["error"]+res.Statuses["500"] >= 40
		mu.Unlock()
		if broken {
			break
		}
		slots <- struct{}{}
		pending.Add(1)
		go func(i int) {
			defer pending.Done()
			defer func() { <-slots }()
			id := fmt.Sprintf("base-replacement-%d", i)
			hreq, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+inst.Lease.HostIP.String()+":8080/", nil)
			hreq.Header.Set("X-Faas-Invocation-Id", id)
			status := "error"
			var body struct {
				OK     bool   `json:"ok"`
				ID     string `json:"invocation_id"`
				Task   int    `json:"task"`
				Digest string `json:"digest"`
				PID    int    `json:"pid"`
			}
			if resp, err := client.Do(hreq); err == nil {
				status = fmt.Sprint(resp.StatusCode)
				decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&body)
				_ = resp.Body.Close()
				if resp.StatusCode == 200 && (decodeErr != nil || !body.OK || body.ID != id || body.Digest == "") {
					status = "invalid_response"
				}
			}
			mu.Lock()
			defer mu.Unlock()
			res.Statuses[status]++
			if status == "200" {
				res.PIDs[body.PID]++
				if !containsString(res.Digests[body.Task], body.Digest) {
					res.Digests[body.Task] = append(res.Digests[body.Task], body.Digest)
				}
			}
		}(i)
	}
	pending.Wait()
	time.Sleep(2 * time.Second) // let late console lines land
	close(stop)
	<-done
	for task, digests := range res.Digests {
		if len(digests) > 1 {
			res.Inconsistent = append(res.Inconsistent, task)
		}
	}
	record("load_complete")
	t.Logf("BASE_REPLACEMENT replaced=%v fresh=%v method=%s statuses=%v inconsistent=%d runtime_errors=%d",
		res.Replaced, res.FreshManager, res.Method, res.Statuses, len(res.Inconsistent), len(res.RuntimeErrors))
}

func copyFileForDiagnostic(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

func inodeOf(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Ino
	}
	return 0
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
