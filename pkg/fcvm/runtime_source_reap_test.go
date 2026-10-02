package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func runtimeSourceOrphanFixture(t *testing.T, owners ...string) (*runtimeSourceCache, string) {
	t.Helper()
	cache := newRuntimeSourceCache()
	cache.parent = filepath.Join(t.TempDir(), "sources")
	for _, owner := range owners {
		if err := cache.retainOwner(owner); err != nil {
			t.Fatal(err)
		}
	}
	if err := cache.ensureRootLocked(); err != nil {
		t.Fatal(err)
	}
	root := cache.root
	if err := os.WriteFile(filepath.Join(root, "faas-snap-runtime-orphan"), []byte("sealed bytes"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := cache.lock.Close(); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(root, old, old); err != nil {
		t.Fatal(err)
	}
	return cache, root
}

func TestRuntimeSourceRestartReapKeepsEveryLiveOrUnknownConsumer(t *testing.T) {
	for _, test := range []struct {
		name string
		live LiveInstanceFunc
		want RuntimeSourceReapReport
	}{
		{"all dead", alwaysDead, RuntimeSourceReapReport{Scanned: 1, Reaped: 1, ReclaimedLogicalBytes: 12}},
		{"one live", func(_ context.Context, id string) (bool, error) { return id == idLive, nil }, RuntimeSourceReapReport{Scanned: 1, SkippedLive: 1}},
		{"unknown", func(context.Context, string) (bool, error) { return false, errors.New("database unavailable") }, RuntimeSourceReapReport{Scanned: 1, SkippedUnknown: 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cache, root := runtimeSourceOrphanFixture(t, idDead, idLive)
			rep, err := ReapOrphanedRuntimeSources(t.Context(), RuntimeSourceReapOptions{Root: cache.parent, IsLive: test.live})
			if err != nil || rep != test.want {
				t.Fatalf("report=%+v err=%v want=%+v", rep, err, test.want)
			}
			_, statErr := os.Stat(root)
			if (test.want.Reaped == 1) != os.IsNotExist(statErr) {
				t.Fatalf("retention=%v", statErr)
			}
		})
	}
}

func TestRuntimeSourceRestartReapSkipsActiveCacheAndRechecksDeadOwner(t *testing.T) {
	cache, root := runtimeSourceOrphanFixture(t, idLive)
	if locked, err := cache.lock.TryLock(); err != nil || !locked {
		t.Fatal("could not reacquire process lock", err)
	}
	t.Cleanup(func() { _ = cache.lock.Close() })
	opts := RuntimeSourceReapOptions{Root: cache.parent, IsLive: alwaysDead}
	rep, err := ReapOrphanedRuntimeSources(t.Context(), opts)
	if err != nil || rep.SkippedActive != 1 || rep.Reaped != 0 {
		t.Fatalf("active cache reaped: %+v %v", rep, err)
	}
	if err := cache.lock.Close(); err != nil {
		t.Fatal(err)
	}
	live := true
	opts.IsLive = func(context.Context, string) (bool, error) { return live, nil }
	rep, err = ReapOrphanedRuntimeSources(t.Context(), opts)
	if err != nil || rep.SkippedLive != 1 {
		t.Fatalf("surviving VM source removed: %+v %v", rep, err)
	}
	live = false
	rep, err = ReapOrphanedRuntimeSources(t.Context(), opts)
	if err != nil || rep.Reaped != 1 {
		t.Fatalf("subsequent dead owner not reclaimed: %+v %v", rep, err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("orphan remains", err)
	}
}

func TestRuntimeSourceRestartReapRejectsMalformedOwnershipAndSymlinks(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*testing.T, string)
	}{
		{"unknown version", func(t *testing.T, root string) {
			t.Helper()
			mustWriteRuntimeRecord(t, root, `{"version":2,"instance_id":"`+idDead+`"}`)
		}},
		{"trailing record", func(t *testing.T, root string) {
			t.Helper()
			mustWriteRuntimeRecord(t, root, `{"version":1,"instance_id":"`+idDead+`"} {}`)
		}},
		{"unknown field", func(t *testing.T, root string) {
			t.Helper()
			mustWriteRuntimeRecord(t, root, `{"version":1,"instance_id":"`+idDead+`","authority":true}`)
		}},
		{"duplicate identity", func(t *testing.T, root string) {
			t.Helper()
			mustWriteRuntimeRecord(t, root, `{"version":1,"instance_id":"`+idLive+`","instance_id":"`+idDead+`"}`)
		}},
		{"invalid owner", func(t *testing.T, root string) {
			t.Helper()
			mustWriteRuntimeRecord(t, root, `{"version":1,"instance_id":"../outside"}`)
		}},
		{"oversized", func(t *testing.T, root string) {
			t.Helper()
			mustWriteRuntimeRecord(t, root, string(make([]byte, 1025)))
		}},
		{"unrecognized file", func(t *testing.T, root string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(root, "unrelated"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlink", func(t *testing.T, root string) {
			t.Helper()
			if err := os.Symlink(t.TempDir(), filepath.Join(root, "faas-snap-runtime-link")); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cache, root := runtimeSourceOrphanFixture(t, idDead)
			test.edit(t, root)
			old := time.Now().Add(-time.Hour)
			if err := os.Chtimes(root, old, old); err != nil {
				t.Fatal(err)
			}
			rep, err := ReapOrphanedRuntimeSources(t.Context(), RuntimeSourceReapOptions{Root: cache.parent, IsLive: alwaysDead})
			if err != nil || rep.SkippedUnknown != 1 || rep.Reaped != 0 {
				t.Fatalf("unsafe reclaim: %+v %v", rep, err)
			}
			if _, err := os.Stat(root); err != nil {
				t.Fatal("unknown ownership removed", err)
			}
		})
	}
}

func mustWriteRuntimeRecord(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, runtimeSourceOwnerName(idDead)), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeSourceRestartReapNeedsGateAndAgeAndHonorsCancellation(t *testing.T) {
	cache, root := runtimeSourceOrphanFixture(t)
	if _, err := ReapOrphanedRuntimeSources(t.Context(), RuntimeSourceReapOptions{Root: cache.parent}); err == nil {
		t.Fatal("nil durable gate accepted")
	}
	if err := os.Chtimes(root, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	opts := RuntimeSourceReapOptions{Root: cache.parent, IsLive: alwaysDead}
	rep, err := ReapOrphanedRuntimeSources(t.Context(), opts)
	if err != nil || rep.SkippedYoung != 1 {
		t.Fatalf("young root reaped: %+v %v", rep, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ReapOrphanedRuntimeSources(ctx, opts); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled reap continued", err)
	}
}

func TestRuntimeSourceOwnershipPrecedesStorageReadAndDropsOnLastRelease(t *testing.T) {
	cache := newRuntimeSourceCache()
	cache.parent = filepath.Join(t.TempDir(), "sources")
	body := []byte("approved")
	source := runtimeSourceFixture("base-image", "", "base/owned.ext4", body)
	backend := &runtimeSourceTestBackend{open: func(context.Context, string) (io.ReadCloser, error) {
		owners, _, err := runtimeSourceOwners(cache.root)
		if err != nil || len(owners) != 1 || owners[0] != idDead {
			t.Fatalf("source read before durable ownership: owners=%v err=%v", owners, err)
		}
		return io.NopCloser(bytes.NewReader(body)), nil
	}}
	if _, err := cache.acquire(t.Context(), backend, idDead, source); err != nil {
		t.Fatal(err)
	}
	root := cache.root
	if err := cache.release(idDead); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) || cache.lock != nil || len(cache.owners) != 0 {
		t.Fatal("last release retained root, lock, or ownership", err)
	}
}

func TestRuntimeSourceOwnershipRefusesUnsafeParentBeforeStorageRead(t *testing.T) {
	outside := t.TempDir()
	link := filepath.Join(t.TempDir(), "sources")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	cache := newRuntimeSourceCache()
	cache.parent = link
	source := runtimeSourceFixture("base-image", "", "base/a.ext4", []byte("approved"))
	backend := &runtimeSourceTestBackend{}
	if _, err := cache.acquire(t.Context(), backend, idDead, source); err == nil || backend.gets.Load() != 0 {
		t.Fatal("unsafe parent read storage", err)
	}
	if _, err := ReapOrphanedRuntimeSources(t.Context(), RuntimeSourceReapOptions{Root: link, IsLive: alwaysDead}); err == nil {
		t.Fatal("symlink parent enabled cleanup")
	}
	files, err := os.ReadDir(outside)
	if err != nil || len(files) != 0 {
		t.Fatal("unsafe parent was modified", err)
	}
}

const runtimeSourceChildParentEnv = "GREGALE_TEST_RUNTIME_SOURCE_CHILD_PARENT"

func TestRuntimeSourceProcessCrashReleasesLock(t *testing.T) {
	if parent := os.Getenv(runtimeSourceChildParentEnv); parent != "" {
		runtimeSourceCrashChild(t, parent)
		return
	}
	parent := filepath.Join(t.TempDir(), "sources")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestRuntimeSourceProcessCrashReleasesLock$")
	cmd.Env = append(os.Environ(), runtimeSourceChildParentEnv+"="+parent)
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	var root string
	if err := json.NewDecoder(stdout).Decode(&root); err != nil {
		t.Fatal("child did not retain sealed source", err, diagnostics.String())
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(root, old, old); err != nil {
		t.Fatal(err)
	}
	opts := RuntimeSourceReapOptions{Root: parent, IsLive: alwaysDead}
	if rep, err := ReapOrphanedRuntimeSources(t.Context(), opts); err != nil || rep.SkippedActive != 1 {
		t.Fatalf("live child not protected: %+v %v", rep, err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("child was not killed")
	}
	if rep, err := ReapOrphanedRuntimeSources(t.Context(), opts); err != nil || rep.Reaped != 1 || rep.ReclaimedLogicalBytes != 8 {
		t.Fatalf("crashed process source leaked: %+v %v", rep, err)
	}
}

func runtimeSourceCrashChild(t *testing.T, parent string) {
	t.Helper()
	cache := newRuntimeSourceCache()
	cache.parent = parent
	body := []byte("approved")
	source := runtimeSourceFixture("base-image", "", "base/crash.ext4", body)
	backend := &runtimeSourceTestBackend{data: map[string][]byte{source.StorageKey: body}}
	if _, err := cache.acquire(t.Context(), backend, idDead, source); err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(cache.root); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}
