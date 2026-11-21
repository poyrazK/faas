//go:build metal

package runlifecycle_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imaged"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestSourceTemplateClonesMetal(t *testing.T) {
	f := newRunFixtureWithTemplates(t, true)
	var snapshot sched.RuntimeSnapshot
	if !t.Run("automatic_capture_and_publication", func(t *testing.T) {
		key, err := f.identity.Key()
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(90 * time.Second)
		index := sched.NewStateRuntimeSnapshotIndex(state.NewPgStore(f.h.Pool))
		for {
			snapshot, err = index.LookupRuntimeSnapshot(t.Context(), key)
			if err == nil {
				break
			}
			if !errors.Is(err, sched.ErrRuntimeSnapshotNotFound) || time.Now().After(deadline) {
				t.Fatalf("automatic publication: %v", err)
			}
			time.Sleep(100 * time.Millisecond)
		}
		// Ensure must discover the automatic worker's durable row, without
		// stopping schedd or manually inserting trust markers in PostgreSQL.
		got := f.capturePristine(t)
		if got.StorageKey != snapshot.StorageKey || got.SnapshotDigest != snapshot.SnapshotDigest {
			t.Fatal("automatic publication was replaced")
		}
		if strings.Count(f.h.VmmdLogs(), `"msg":"execution template captured"`) != 1 {
			t.Fatal("expected exactly one native template producer")
		}
		for _, key := range templateObjectKeys(snapshot.StorageKey) {
			info, err := os.Stat(filepath.Join(f.nativeRoot, key))
			if err != nil {
				t.Fatal(err)
			}
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok || stat.Uid != 0 || info.Mode().Perm()&0o022 != 0 {
				t.Fatal("template artifact is writable by another principal")
			}
			if _, err := os.Lstat(filepath.Join(f.storageRoot, key)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("template was also published into shared storage")
			}
		}
	}) {
		return
	}
	if !t.Run("idempotent_publication_after_restart", func(t *testing.T) {
		if err := f.h.KillSchedd(); err != nil {
			t.Fatal(err)
		}
		if err := f.h.RestartSchedd(); err != nil {
			t.Fatal(err)
		}
		got := f.capturePristine(t)
		if got.StorageKey != snapshot.StorageKey || got.SnapshotDigest != snapshot.SnapshotDigest {
			t.Fatal("restart changed the immutable publication")
		}
		if strings.Count(f.h.VmmdLogs(), `"msg":"execution template captured"`) != 1 {
			t.Fatal("restart captured a second template")
		}
	}) {
		return
	}
	if !t.Run("failed_capture_never_publishes", func(t *testing.T) {
		spec := f.templateSpec(t)
		spec.Identity.KernelDigest = strings.Repeat("f", 64)
		spec.Artifacts.KernelDigest = spec.Identity.KernelDigest
		backend, err := f.snapshotBackend()
		if err != nil {
			t.Fatal(err)
		}
		conn, err := wire.DialContext(t.Context(), f.h.VMMDSock, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		_, err = sched.NewExecutionTemplatePublisher(state.NewPgStore(f.h.Pool), backend, sched.NewVMMClient(conn)).Ensure(t.Context(), spec)
		if err == nil {
			t.Fatal("wrong kernel pin published a template")
		}
		key, _ := spec.Identity.Key()
		if _, err := state.NewPgStore(f.h.Pool).LookupRuntimeSnapshot(t.Context(), key); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("failed capture left a catalog row: %v", err)
		}
		stats, err := vmmdpb.NewVmmdClient(conn).Stats(t.Context(), &vmmdpb.StatsRequest{})
		if err != nil || stats.GetLiveCount() != 0 || len(stats.GetInstances()) != 0 {
			t.Fatalf("failed capture retained a VM: %v %v", stats, err)
		}
	}) {
		return
	}
	if !t.Run("catalog_retention_and_orphan_gc", func(t *testing.T) {
		old := time.Now().Add(-48 * time.Hour)
		captureDir := filepath.Dir(filepath.Dir(filepath.Join(f.nativeRoot, snapshot.StorageKey)))
		if err := os.Chtimes(captureDir, old, old); err != nil {
			t.Fatal(err)
		}
		orphan := state.SnapshotCaptureMemKey(uuid.Nil.String(), "", uuid.NewString())
		for _, key := range templateObjectKeys(orphan) {
			path := filepath.Join(f.nativeRoot, key)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("inert-unpublished-capture"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		orphanDir := filepath.Dir(filepath.Dir(filepath.Join(f.nativeRoot, orphan)))
		if err := os.Chtimes(orphanDir, old, old); err != nil {
			t.Fatal(err)
		}
		gcClient := imaged.NewVMMClient(f.h.VMMDSock, nil)
		defer gcClient.Close()
		handler := (&imaged.Handler{}).WithNativeRuntimeSnapshotRoot(f.nativeRoot).WithVMMClient(gcClient)
		loop := imaged.NewLoop(imaged.LoopConfig{Handler: handler, Store: state.NewPgStore(f.h.Pool), StorageRoot: f.storageRoot})
		if err := loop.CollectExecutionTemplateOrphans(t.Context(), time.Now()); err != nil {
			t.Fatal(err)
		}
		for _, key := range templateObjectKeys(orphan) {
			if _, err := os.Lstat(filepath.Join(f.nativeRoot, key)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("orphan survived: %s: %v", key, err)
			}
		}
		backend, err := f.snapshotBackend()
		if err != nil {
			t.Fatal(err)
		}
		if err := sched.NewStorageRuntimeSnapshotVerifier(backend).VerifyRuntimeSnapshot(t.Context(), snapshot); err != nil {
			t.Fatal(err)
		}
		drift, err := sched.NewDiskDrift(state.NewPgStore(f.h.Pool), nil).WithStorage(backend).Tick(t.Context())
		if err != nil || drift != 0 {
			t.Fatalf("catalogued template drift=%d error=%v", drift, err)
		}
	}) {
		return
	}
	if !t.Run("four_live_clones_are_isolated", func(t *testing.T) { f.concurrentClones(t, 4, false) }) {
		return
	}
	if !t.Run("cancelling_one_clone_spares_its_peer", func(t *testing.T) { f.concurrentClones(t, 2, true) }) {
		return
	}
	for _, part := range []string{"drive", "backing"} {
		if !t.Run("corrupt_"+part+"_cold_fallback", func(t *testing.T) {
			key := state.SnapshotDriveKey(state.Snapshot{StorageKey: snapshot.StorageKey})
			if part == "backing" {
				key = state.SnapshotBackingKey(state.Snapshot{StorageKey: snapshot.StorageKey})
			}
			file, err := os.OpenFile(filepath.Join(f.nativeRoot, key), os.O_RDWR, 0) //nolint:forbidigo // Corrupt only this test's private native template, never a shared runtime image.
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
			defer func() {
				if _, err := file.WriteAt(original[:], 0); err != nil {
					t.Error(err)
				}
			}()
			row := f.wait(t, f.submit(t, "corrupt-template-"+part, runRequest(`export default async()=>({answer:42})`)).ID, api.ExecutionStatusSucceeded)
			f.requireWakeMethod(t, row.ID, "cold_boot")
			var result struct{ Answer int }
			if json.Unmarshal(row.Result, &result) != nil || result.Answer != 42 {
				t.Fatal("cold fallback lost its result")
			}
		}) {
			return
		}
	}
	t.Run("template_unchanged_and_usage_settled", func(t *testing.T) {
		f.requireCleanRestore(t, "after-concurrent-clones")
		got := f.capturePristine(t)
		if got.StorageKey != snapshot.StorageKey || got.SnapshotDigest != snapshot.SnapshotDigest {
			t.Fatal("clones mutated their template")
		}
		usage, err := state.NewPgStore(f.h.Pool).ExecutionUsageByAccount(t.Context(), f.templateAccountID, time.Now().UTC())
		if err != nil || usage.Runs != 0 {
			t.Fatalf("template machinery charged customer Runs: %+v %v", usage, err)
		}
		f.assertSettled(t)
	})
}

func templateObjectKeys(mem string) []string {
	s := state.Snapshot{StorageKey: mem}
	return []string{mem, state.SnapshotVMStateKey(s), state.SnapshotDriveKey(s), state.SnapshotBackingKey(s)}
}

func (f *runFixture) concurrentClones(t *testing.T, count int, cancelOne bool) {
	t.Helper()
	ids := make([]string, count)
	secrets := make([]string, count)
	for i := range count {
		req := runRequest(`import fs from 'node:fs';import crypto from 'node:crypto';import os from 'node:os';export default async(input)=>{const clean=!fs.existsSync('/tmp/clone-private');fs.writeFileSync('/tmp/clone-private',input.secret);const started=Date.now(),nonce=crypto.randomBytes(32).toString('hex');console.log('running-marker');await new Promise(r=>setTimeout(r,12000));return {clean,secret:fs.readFileSync('/tmp/clone-private','utf8'),uid:process.getuid(),nonce,started,finished:Date.now(),interfaces:Object.keys(os.networkInterfaces())};}`)
		secrets[i] = uuid.NewString()
		req.Input, _ = json.Marshal(map[string]string{"secret": secrets[i]})
		ids[i] = f.submit(t, uuid.NewString(), req).ID
	}
	for _, id := range ids {
		f.waitGuestStarted(t, id)
	}
	f.assertSimultaneouslyIsolated(t, ids)
	if cancelOne {
		if _, err := f.client.CancelExecution(t.Context(), ids[0]); err != nil {
			t.Fatal(err)
		}
	}
	nonces := map[string]bool{}
	var latestStart, earliestFinish int64
	for i, id := range ids {
		want := api.ExecutionStatusSucceeded
		if cancelOne && i == 0 {
			want = api.ExecutionStatusCancelled
		}
		row := f.wait(t, id, want)
		f.requireWakeMethod(t, id, "restore")
		if want == api.ExecutionStatusCancelled {
			continue
		}
		var result struct {
			Clean             bool
			Secret, Nonce     string
			UID               int
			Started, Finished int64
			Interfaces        []string
		}
		if err := json.Unmarshal(row.Result, &result); err != nil || !result.Clean || result.Secret != secrets[i] || result.UID != 1000 || len(result.Nonce) != 64 || nonces[result.Nonce] {
			t.Fatalf("clone isolation result failed for %s: %v", id, err)
		}
		for _, name := range result.Interfaces {
			if name != "lo" {
				t.Fatalf("clone acquired interface %q", name)
			}
		}
		nonces[result.Nonce] = true
		latestStart = max(latestStart, result.Started)
		if earliestFinish == 0 || result.Finished < earliestFinish {
			earliestFinish = result.Finished
		}
	}
	if latestStart >= earliestFinish {
		t.Fatal("clone execution windows did not overlap")
	}
	t.Logf("concurrent clone witness: count=%d unique_rng=%d cancelled=%t overlapping=true", count, len(nonces), cancelOne)
}

func (f *runFixture) assertSimultaneouslyIsolated(t *testing.T, ids []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	conn, err := wire.DialContext(ctx, f.h.VMMDSock, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := vmmdpb.NewVmmdClient(conn)
	stats, err := client.Stats(ctx, &vmmdpb.StatsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.GetLiveCount() < int32(len(ids)) {
		t.Fatalf("simultaneous clone live count=%d, want at least %d", stats.GetLiveCount(), len(ids))
	}
	seen := map[int]bool{}
	roots, drives, cgroups := map[string]bool{}, map[string]bool{}, map[string]bool{}
	inode := func(path string) string {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			t.Fatal("missing Linux inode identity")
		}
		return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino)
	}
	for _, id := range ids {
		status, err := client.SeccompStatus(ctx, &vmmdpb.SeccompStatusRequest{Instance: id})
		if err != nil || status.GetPid() <= 0 || status.GetMode() != "filter" || status.GetError() != "" {
			t.Fatalf("clone process/seccomp evidence unavailable: %v", err)
		}
		wantUID := f.requireWakeMethod(t, id, "restore")
		processStatus, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", status.GetPid())) //nolint:forbidigo // This test's VM PID, returned by authenticated vmmd.
		if err != nil {
			t.Fatal(err)
		}
		uid := -1
		for _, line := range strings.Split(string(processStatus), "\n") {
			if strings.HasPrefix(line, "Uid:") {
				fields := strings.Fields(line)
				if len(fields) != 5 {
					t.Fatal("invalid process UID evidence")
				}
				uid, err = strconv.Atoi(fields[1])
				if err != nil {
					t.Fatal(err)
				}
				for _, field := range fields[2:] {
					other, err := strconv.Atoi(field)
					if err != nil || other != uid {
						t.Fatal("process has inconsistent UID credentials")
					}
				}
			}
		}
		if uid != wantUID || uid <= 0 || seen[uid] {
			t.Fatalf("live clone UID=%d, wake UID=%d, duplicate=%t", uid, wantUID, seen[uid])
		}
		seen[uid] = true
		root := fmt.Sprintf("/proc/%d/root", status.GetPid())
		rootID, driveID := inode(root), inode(filepath.Join(root, "layer.ext4"))
		if roots[rootID] || drives[driveID] {
			t.Fatal("live clones shared a jail root or writable drive")
		}
		roots[rootID] = true
		drives[driveID] = true
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", status.GetPid()))
		if err != nil {
			t.Fatal(err)
		} //nolint:forbidigo // PID comes from vmmd for this test's admitted Run.
		path, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "0::/")
		if !ok || strings.Contains(path, "..") || path == "" {
			t.Fatal("invalid cgroup v2 evidence")
		}
		group := filepath.Join("/sys/fs/cgroup", path)
		groupID := inode(group)
		if cgroups[groupID] {
			t.Fatal("live clones shared a cgroup")
		}
		cgroups[groupID] = true
		limit, err := os.ReadFile(filepath.Join(group, "memory.max"))
		if err != nil || strings.TrimSpace(string(limit)) != fmt.Sprint((f.identity.MemoryMB+api.PerVMOverheadMB)<<20) {
			t.Fatalf("clone memory cap not enforced: %q %v", limit, err)
		} //nolint:forbidigo // Validated cgroup of this test's owned Firecracker PID.
	}
	t.Logf("simultaneous native isolation: live=%d unique_jail_uids=%d unique_roots=%d unique_writable_drives=%d unique_cgroups=%d seccomp=filter", len(ids), len(seen), len(roots), len(drives), len(cgroups))
}
