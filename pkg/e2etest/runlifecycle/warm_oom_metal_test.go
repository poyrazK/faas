//go:build metal

package runlifecycle_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"

	"github.com/onebox-faas/faas/pkg/wire"
)

// This cohort uses a fresh operator-owned guest, captured before any Execute
// RPC or caller payload exists. It exercises the real catalog and scheduler;
// an unnoticed cold fallback is a test failure in every warm case.
func TestSourceWarmRestoreOOMMetal(t *testing.T) {
	f := newRunFixture(t)
	var snapshot sched.RuntimeSnapshot
	if !t.Run("cold_real_oom", func(t *testing.T) { f.requireOOM(t, "cold-oom", "cold_boot") }) {
		return
	}
	if !t.Run("payload_free_capture", func(t *testing.T) { snapshot = f.capturePristine(t) }) {
		return
	}
	if !t.Run("repeated_restores_are_clean", func(t *testing.T) {
		// Make the stopped-clock error exceed the assertion's tolerance.
		time.Sleep(6 * time.Second)
		var previous string
		for i := range 2 {
			row, nonce := f.requireCleanRestore(t, fmt.Sprintf("warm-clean-%d", i))
			if nonce == previous {
				t.Fatal("restores repeated their random bytes")
			}
			previous = nonce
			t.Logf("warm restore witness: run=%s nonce=%s", row.ID, nonce)
		}
	}) {
		return
	}
	if !t.Run("warm_real_oom_and_recovery", func(t *testing.T) {
		f.requireOOM(t, "warm-oom", "restore")
		f.requireCleanRestore(t, "warm-after-oom")
	}) {
		return
	}
	for _, part := range []string{"memory", "vmstate"} {
		if !t.Run("corrupt_"+part+"_cold_fallback", func(t *testing.T) {
			key := snapshot.StorageKey
			if part == "vmstate" {
				key = state.SnapshotVMStateKey(state.Snapshot{StorageKey: key})
			}
			path := filepath.Join(f.nativeRoot, filepath.FromSlash(key))
			file, err := os.OpenFile(path, os.O_RDWR, 0) //nolint:forbidigo // Fault injection into this operator-owned private capture only.
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			var original [1]byte
			if _, err := file.ReadAt(original[:], 0); err != nil {
				t.Fatal(err)
			}
			if _, err := file.WriteAt([]byte{original[0] ^ 1}, 0); err != nil {
				t.Fatal(err)
			}
			// Restore the fault before closing this private capture's fd.
			defer func() {
				if _, err := file.WriteAt(original[:], 0); err != nil {
					t.Error(err)
				}
			}()
			row := f.wait(t, f.submit(t, "corrupt-"+part, runRequest(`export default async()=>({answer:42})`)).ID, api.ExecutionStatusSucceeded)
			f.requireWakeMethod(t, row.ID, "cold_boot")
			var result struct{ Answer int }
			if err := json.Unmarshal(row.Result, &result); err != nil || result.Answer != 42 {
				t.Fatalf("cold fallback result: %s, %v", row.Result, err)
			}
		}) {
			return
		}
	}
	t.Run("payload_erasure_and_single_usage_entry", func(t *testing.T) { f.assertSettled(t) })
}

func (f *runFixture) templateSpec(t *testing.T) sched.ExecutionTemplateSpec {
	t.Helper()
	store := state.NewPgStore(f.h.Pool)
	if f.templateAccountID == "" {
		account, err := store.CreateAccount(t.Context(), "run-template-"+uuid.NewString()+"@example.test", api.PlanScale)
		if err != nil {
			t.Fatal(err)
		}
		f.templateAccountID = account.ID
	}
	nodes, err := store.ActiveComputeNodes(t.Context())
	if err != nil || len(nodes) != 1 {
		t.Fatalf("template node: count=%d err=%v", len(nodes), err)
	}
	return sched.ExecutionTemplateSpec{AccountID: f.templateAccountID, NodeID: nodes[0].ID, Identity: f.identity, Artifacts: f.artifacts}
}

func (f *runFixture) capturePristine(t *testing.T) sched.RuntimeSnapshot {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(f.storageRoot, "snap"), 0o700); err != nil {
		t.Fatal(err)
	}
	backend, err := f.snapshotBackend()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := wire.DialContext(t.Context(), f.h.VMMDSock, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	publisher := sched.NewExecutionTemplatePublisher(state.NewPgStore(f.h.Pool), backend, sched.NewVMMClient(conn))
	entry, err := publisher.Ensure(t.Context(), f.templateSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := sched.NewStorageRuntimeSnapshotVerifier(backend).VerifyRuntimeSnapshot(t.Context(), entry); err != nil {
		t.Fatal(err)
	}
	driveKey := state.SnapshotDriveKey(state.Snapshot{StorageKey: entry.StorageKey})
	t.Logf("production payload-free capture: snapshot_digest=%s private_drive_sha256=%s memory_bytes=%d vmstate_bytes=%d", entry.SnapshotDigest, hashRunFile(t, filepath.Join(f.nativeRoot, driveKey)), entry.MemBytes, entry.VMStateBytes)
	return entry
}

func (f *runFixture) requireWakeMethod(t *testing.T, id, want string) int {
	t.Helper()
	for _, line := range strings.Split(f.h.VmmdLogs(), "\n") {
		var event struct {
			Message  string `json:"msg"`
			Instance string `json:"instance"`
			Method   string `json:"method"`
			UID      int    `json:"uid"`
		}
		if json.Unmarshal([]byte(line), &event) == nil && event.Message == "wake ok" && event.Instance == id {
			if event.Method != want {
				t.Fatalf("Run %s used %s, want %s", id, event.Method, want)
			}
			f.requireNativeKVMOnlyDevice(t, id)
			t.Logf("wake witness: run=%s method=%s jail_uid=%d", id, event.Method, event.UID)
			return event.UID
		}
	}
	t.Fatalf("Run %s has no native wake-method witness", id)
	return 0
}

func (f *runFixture) requireCleanRestore(t *testing.T, key string) (api.ExecutionResponse, string) {
	t.Helper()
	req := runRequest(`import fs from 'node:fs'; import crypto from 'node:crypto'; import os from 'node:os'; export default async(input)=>{ const clean=!fs.existsSync('/tmp/warm-private')&&!fs.existsSync('/tmp/warm-oom-private')&&!fs.existsSync('/tmp/clone-private'); fs.writeFileSync('/tmp/warm-private',input.secret); return {clean,secret:input.secret,uid:process.getuid(),nonce:crypto.randomBytes(32).toString('hex'),now:Date.now(),interfaces:Object.keys(os.networkInterfaces())}; }`)
	secret := uuid.NewString() // Created after the pristine capture was published.
	req.Input, _ = json.Marshal(map[string]string{"secret": secret})
	row := f.wait(t, f.submit(t, key, req).ID, api.ExecutionStatusSucceeded)
	f.requireWakeMethod(t, row.ID, "restore")
	var result struct {
		Clean         bool
		Secret, Nonce string
		UID           int
		Now           int64
		Interfaces    []string
	}
	if err := json.Unmarshal(row.Result, &result); err != nil || !result.Clean || result.Secret != secret || result.UID != 1000 || len(result.Nonce) != 64 || time.Since(time.UnixMilli(result.Now)).Abs() > 2*time.Second {
		t.Fatalf("unclean restored result: %s, %v", row.Result, err)
	}
	for _, name := range result.Interfaces {
		if name != "lo" {
			t.Fatalf("restored network interface %q", name)
		}
	}
	return row, result.Nonce
}

func (f *runFixture) requireOOM(t *testing.T, key, method string) {
	t.Helper()
	req := runRequest(`import fs from 'node:fs'; export default async()=>{fs.writeFileSync('/tmp/warm-oom-private','private'); console.log('running-marker'); await new Promise(r=>setTimeout(r,1000)); const chunks=[]; while(true){chunks.push(Buffer.alloc(8*1024*1024,0xa5)); await new Promise(r=>setTimeout(r,5));}}`)
	row := f.submit(t, key, req)
	f.waitGuestStarted(t, row.ID)
	console, err := os.Open(filepath.Join("/var/log/faas", "vm-"+row.ID+".console")) //nolint:forbidigo // This test's admitted UUID; pin the console across native teardown.
	if err != nil {
		t.Fatal(err)
	}
	defer console.Close()
	row = f.wait(t, row.ID, api.ExecutionStatusOutOfMemory)
	f.requireWakeMethod(t, row.ID, method)
	if row.Failure == nil || row.Failure.Code != "out_of_memory" || row.Usage == nil || row.Usage.WallTimeMS <= 0 {
		t.Fatalf("OOM receipt lost its failure/usage: %+v", row)
	}
	data, err := io.ReadAll(io.LimitReader(console, 256<<10))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("execution interpreter killed by guest OOM")) {
		t.Fatalf("no trusted guest OOM counter witness: %s", data)
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		if bytes.Contains(line, []byte("execution interpreter killed by guest OOM")) {
			t.Logf("guest kernel OOM witness: %s", line)
		}
	}
}

func (f *runFixture) snapshotBackend() (*sched.RuntimeSnapshotReader, error) {
	canonical, err := storage.NewLocalStorageBackend(f.storageRoot)
	if err != nil {
		return nil, err
	}
	return sched.NewRuntimeSnapshotReader(canonical, f.nativeRoot)
}
