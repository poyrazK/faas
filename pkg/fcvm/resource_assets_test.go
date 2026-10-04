// adr: 474
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
)

func assetFixture(t *testing.T) (*JailerVMM, *ResourceJournal) {
	t.Helper()
	j := openTestResourceJournal(t, t.TempDir())
	if err := j.begin(journalTestLease(idLive, 0)); err != nil {
		t.Fatal(err)
	}
	v := NewJailerVMM(t.TempDir(), time.Second)
	v.SetResourceJournal(j)
	return v, j
}

func TestResourceAssetsIntentCheckpointAndReopen(t *testing.T) {
	v, j := assetFixture(t)
	syncDir := j.directorySync
	intent, checkpoint := false, false
	j.directorySync = func() error {
		for _, a := range j.records[idLive].Assets {
			info, err := os.Stat(a.Path)
			if a.File == nil {
				if !errors.Is(err, os.ErrNotExist) {
					t.Errorf("file existed before intent commit: %v", err)
				}
				intent = true
			} else {
				if err != nil || info.Size() != 0 {
					t.Errorf("data copy preceded file checkpoint: %v", err)
				}
				checkpoint = true
			}
		}
		return syncDir()
	}
	f, err := v.newMaterialisedFile(idLive, t.TempDir(), "payload-*.bin", "materialised")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("private-payload-fixture"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if !intent || !checkpoint {
		t.Fatal("missing ordered journal commits")
	}
	j.directorySync = syncDir
	record, _, _ := j.lookup(idLive)
	body, _ := json.Marshal(record)
	if strings.Contains(string(body), "private-payload-fixture") {
		t.Fatal("artifact contents entered journal")
	}
	// Storage reopen does not transfer lifecycle ownership; retain the original VMM.
	root := j.dir.Name()
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openTestResourceJournal(t, root)
	v.SetResourceJournal(reopened)
	record, _, err = reopened.lookup(idLive)
	if err != nil || len(record.Assets) != 1 || record.Assets[0].File == nil {
		t.Fatalf("checkpoint lost: %+v %v", record, err)
	}
	copy, _, _ := reopened.lookup(idLive)
	copy.Assets[0].File.Inode++
	again, _, _ := reopened.lookup(idLive)
	if *again.Assets[0].File != *record.Assets[0].File {
		t.Fatal("lookup exposed mutable journal state")
	}
	if err := v.sweepMaterialised(idLive); err != nil {
		t.Fatal(err)
	}
	record, _, _ = reopened.lookup(idLive)
	if len(record.Assets) != 0 {
		t.Fatal("confirmed removal did not retire asset")
	}
	if _, err := os.Stat(f.Name()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("file survived cleanup")
	}
}

// adr: 474
// adr: 425
func TestResourceAssetsRetainedStorageLink(t *testing.T) {
	for _, phase := range []string{"retained", "copy", "intent", "checkpoint", "retirement", "collision", "replacement"} {
		t.Run(phase, func(t *testing.T) {
			t.Setenv("TMPDIR", t.TempDir())
			v, j := assetFixture(t)
			cache, err := storage.NewLocalCacheBackend(&restoreResolutionBackend{}, t.TempDir(), 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			backend := &observedMaterializationBackend{StorageBackend: cache}
			v.WithStorage(backend)
			injected := errors.New("retained asset fsync failure")
			syncDir := j.directorySync
			intentCommitted, retiring := false, false
			j.directorySync = func() error {
				assets := j.records[idLive].Assets
				if (phase == "intent" && len(assets) == 1 && assets[0].File == nil) ||
					(phase == "checkpoint" && len(assets) == 1 && assets[0].File != nil) ||
					(phase == "retirement" && retiring && len(assets) == 0) {
					return injected
				}
				if err := syncDir(); err != nil {
					return err
				}
				if len(assets) == 1 && assets[0].File == nil {
					intentCommitted = true
				}
				return nil
			}
			backend.beforeLink = func() {
				record, _, err := j.lookup(idLive)
				if err != nil || !intentCommitted || len(record.Assets) != 1 || record.Assets[0].File != nil {
					t.Fatalf("link preceded durable intent: %+v, %v", record, err)
				}
				if _, err := os.Lstat(record.Assets[0].Path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("destination existed before link: %v", err)
				}
				if phase == "collision" {
					if err := os.WriteFile(record.Assets[0].Path, []byte("foreign"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if phase == "copy" {
				backend.linkErr = syscall.EXDEV
			}
			path, err := v.materializeFromStorage(context.Background(), idLive, "snap/dep/mem")
			j.directorySync = syncDir
			if phase == "intent" || phase == "checkpoint" {
				if !errors.Is(err, injected) || backend.reads.Load() != 0 {
					t.Fatalf("uncertain link fell through to copying: %v, reads=%d", err, backend.reads.Load())
				}
				want := 0
				if phase == "checkpoint" {
					want = 1
				}
				if len(v.materialisedTmp[idLive]) != want {
					t.Fatal("checkpoint failure lost cleanup ownership")
				}
			} else if phase == "collision" {
				if err == nil || len(v.materialisedTmp[idLive]) != 1 || v.sweepMaterialised(idLive) == nil {
					t.Fatal("foreign destination granted cleanup authority")
				}
				path = v.materialisedTmp[idLive][0]
				got, err := os.ReadFile(path)
				if err != nil || string(got) != "foreign" {
					t.Fatal("cleanup touched the competing destination")
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				if err != nil || (backend.reads.Load() == 0) != (phase != "copy") {
					t.Fatalf("materialization path changed: %v, reads=%d", err, backend.reads.Load())
				}
				record, _, err := j.lookup(idLive)
				if err != nil || len(record.Assets) != 1 || record.Assets[0].File == nil || record.Assets[0].Path != path {
					t.Fatalf("retained file lacks inode checkpoint: %+v, %v", record, err)
				}
				if err := cache.Delete(context.Background(), "snap/dep/mem"); err != nil {
					t.Fatal(err)
				}
				got, err := os.ReadFile(path)
				if err != nil || string(got) != "remote" {
					t.Fatal("cache eviction removed retained bytes")
				}
				if phase == "replacement" {
					if err := os.Rename(path, path+".original"); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte("foreign"), 0o600); err != nil {
						t.Fatal(err)
					}
					if v.sweepMaterialised(idLive) == nil {
						t.Fatal("replacement accepted as owned inode")
					}
					if got, err := os.ReadFile(path); err != nil || string(got) != "foreign" {
						t.Fatal("replacement deleted")
					}
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err := os.Rename(path+".original", path); err != nil {
						t.Fatal(err)
					}
				}
				if phase == "retirement" {
					retiring = true
					j.directorySync = func() error { return injected }
					if !errors.Is(v.sweepMaterialised(idLive), injected) || len(v.materialisedTmp[idLive]) != 1 {
						t.Fatal("failed retirement lost retry ownership")
					}
					j.directorySync = syncDir
				}
			}
			if err := v.sweepMaterialised(idLive); err != nil {
				t.Fatal(err)
			}
			if phase != "intent" {
				record, _, err := j.lookup(idLive)
				if err != nil || len(record.Assets) != 0 {
					t.Fatal("confirmed cleanup retained asset intent")
				}
			}
		})
	}
}

func TestResourceAssetsFailedCommitRetainsCleanup(t *testing.T) {
	for _, phase := range []string{"intent", "checkpoint", "retirement"} {
		t.Run(phase, func(t *testing.T) {
			v, j := assetFixture(t)
			syncDir := j.directorySync
			injected := errors.New("asset fsync failure")
			j.directorySync = func() error {
				assets := j.records[idLive].Assets
				if (phase == "intent" && len(assets) > 0 && assets[0].File == nil) || (phase == "checkpoint" && len(assets) > 0 && assets[0].File != nil) {
					return injected
				}
				return syncDir()
			}
			dir := t.TempDir()
			f, err := v.newMaterialisedFile(idLive, dir, "stage-*.bin", "materialised")
			if phase != "retirement" {
				if !errors.Is(err, injected) {
					t.Fatalf("write not rejected: %v", err)
				}
				files, _ := os.ReadDir(dir)
				want := 0
				if phase == "checkpoint" {
					want = 1
				}
				if len(files) != want || len(v.materialisedTmp[idLive]) != want {
					t.Fatal("partial creation lost its cleanup owner")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				_ = f.Close()
				j.directorySync = func() error { return injected }
				if err := v.sweepMaterialised(idLive); !errors.Is(err, injected) {
					t.Fatalf("retirement acknowledged: %v", err)
				}
				if len(v.materialisedTmp[idLive]) != 1 {
					t.Fatal("uncertain retirement lost retry identity")
				}
			}
			j.directorySync = syncDir
			if err := v.sweepMaterialised(idLive); err != nil {
				t.Fatal(err)
			}
			if len(v.materialisedTmp[idLive]) != 0 {
				t.Fatal("confirmed cleanup retained paths")
			}
		})
	}
}

func TestResourceAssetsRefuseReplacedFile(t *testing.T) {
	v, _ := assetFixture(t)
	f, err := v.newMaterialisedFile(idLive, t.TempDir(), "stage-*.bin", "materialised")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	held := f.Name() + ".original"
	if err := os.Rename(f.Name(), held); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.Name(), []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := v.sweepMaterialised(idLive); err == nil {
		t.Fatal("replacement file was removed")
	}
	if body, _ := os.ReadFile(f.Name()); string(body) != "foreign" {
		t.Fatal("replacement file changed")
	}
	_ = os.Remove(f.Name())
	if err := os.Rename(held, f.Name()); err != nil {
		t.Fatal(err)
	}
	if err := v.sweepMaterialised(idLive); err != nil {
		t.Fatal(err)
	}
}

func TestResourceAssetsLegacyAndCorruptRecords(t *testing.T) {
	for _, kind := range []string{"legacy", "legacy_assets", "relative", "duplicate", "unknown", "partial_mount", "namespace_mismatch"} {
		t.Run(kind, func(t *testing.T) {
			_, j := assetFixture(t)
			r, _, _ := j.lookup(idLive)
			identity := resourceFileIdentity{Device: 1, Inode: 2}
			ns := resourceMountIdentity{BootID: idLive, Namespace: 3}
			a := resourceAsset{Kind: "bind", Path: filepath.Join(t.TempDir(), "target"), Source: filepath.Join(t.TempDir(), "source"), SourceFile: &identity, Namespace: &ns, OriginalMode: 0o600}
			r.Assets = []resourceAsset{a}
			switch kind {
			case "legacy":
				r.Version, r.Assets = 1, nil
			case "legacy_assets":
				r.Version = 1
			case "relative":
				r.Assets[0].Path = "relative"
			case "duplicate":
				r.Assets = append(r.Assets, a)
			case "unknown":
				r.Assets[0].Kind = "unknown"
			case "partial_mount":
				r.Assets[0].File = &identity
			case "namespace_mismatch":
				r.Assets[0].File = &identity
				r.Assets[0].Mount = &resourceMountIdentity{BootID: idOther, Namespace: 3, MountID: 4}
			}
			body, _ := json.Marshal(r)
			root := j.dir.Name()
			_ = j.Close()
			if err := os.WriteFile(filepath.Join(root, resourceRecordName(idLive)), body, 0o600); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenResourceJournal(root)
			if kind == "legacy" {
				if err != nil {
					t.Fatal(err)
				}
				_ = reopened.Close()
			} else if err == nil {
				_ = reopened.Close()
				t.Fatal("ambiguous asset record admitted")
			}
		})
	}
}

func TestResourceAssetsBindModeRestoresPinnedInodeAndAliases(t *testing.T) {
	v := NewJailerVMM(t.TempDir(), time.Second)
	path := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(path, []byte("owned"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	identity, _ := resourceFileID(info)
	handle, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	v.bindSourceModes[path] = bindSourceMode{file: identity, mode: 0o600, refs: 1, handle: handle}
	held := path + ".held"
	if err := os.Rename(path, held); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := v.releaseBindSource(path); err != nil {
		t.Fatal(err)
	}
	info, _ = os.Stat(held)
	if info.Mode().Perm() != 0o600 || len(v.bindSourceModes) != 0 {
		t.Fatal("original inode mode was not restored after pathname replacement")
	}
	info, _ = os.Stat(path)
	if info.Mode().Perm() != 0o644 {
		t.Fatal("replacement inode permissions changed")
	}

	aliasSource := filepath.Join(t.TempDir(), "alias-source")
	if err := os.WriteFile(aliasSource, []byte("aliased"), 0o644); err != nil {
		t.Fatal(err)
	}
	alias := aliasSource + ".alias"
	if err := os.Link(aliasSource, alias); err != nil {
		t.Fatal(err)
	}
	aliasInfo, _ := os.Stat(aliasSource)
	aliasIdentity, _ := resourceFileID(aliasInfo)
	primaryHandle, err := os.Open(aliasSource)
	if err != nil {
		t.Fatal(err)
	}
	aliasHandle, err := os.Open(alias)
	if err != nil {
		_ = primaryHandle.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = primaryHandle.Close()
		_ = aliasHandle.Close()
	})
	v.bindSourceModes[aliasSource] = bindSourceMode{file: aliasIdentity, mode: 0o600, refs: 1, handle: primaryHandle}
	v.bindSourceModes[alias] = bindSourceMode{file: aliasIdentity, mode: 0o600, refs: 1, handle: aliasHandle}
	if err := v.releaseBindSource(aliasSource); err != nil {
		t.Fatal(err)
	}
	info, _ = os.Stat(alias)
	if info.Mode().Perm() != 0o644 {
		t.Fatal("permissions restored while alias still referenced")
	}
	if err := v.releaseBindSource(alias); err != nil {
		t.Fatal(err)
	}
	info, _ = os.Stat(aliasSource)
	if info.Mode().Perm() != 0o600 || len(v.bindSourceModes) != 0 {
		t.Fatal("last source reference did not restore mode")
	}
}

func TestResourceAssetsBindModeWaitsForForeignOwner(t *testing.T) {
	v, j := assetFixture(t)
	if err := j.begin(journalTestLease(idOther, 1)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "source")
	_ = os.WriteFile(path, []byte("fixture"), 0o644)
	info, _ := os.Stat(path)
	identity, _ := resourceFileID(info)
	handle, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	foreign := filepath.Join(t.TempDir(), "foreign-target")
	ns := resourceMountIdentity{BootID: idLive, Namespace: 3}
	if err := j.addAsset(idOther, resourceAsset{Kind: "bind", Path: foreign, Source: path, SourceFile: &identity, Namespace: &ns, OriginalMode: 0o600}); err != nil {
		t.Fatal(err)
	}
	v.bindSourceModes[path] = bindSourceMode{file: identity, mode: 0o600, refs: 1, handle: handle}
	if err := v.releaseBindSource(path); err == nil {
		t.Fatal("permissions changed beneath an unknown owner")
	}
	info, _ = os.Stat(path)
	if info.Mode().Perm() != 0o644 || len(v.bindSourceModes) != 1 {
		t.Fatal("unknown owner lost protection")
	}
	if err := j.retireAsset(idOther, foreign); err != nil {
		t.Fatal(err)
	}
	if err := v.releaseBindSource(path); err != nil {
		t.Fatal(err)
	}
}

func TestResourceAssetsMountInfoIdentity(t *testing.T) {
	line := "10 1 0:2 / /tmp/test\\040space rw - ext4 /dev/loop0 rw\n"
	id, found, err := parseResourceMountInfo([]byte(line), "/tmp/test space")
	if err != nil || !found || id != 10 {
		t.Fatalf("mount identity: %d %v %v", id, found, err)
	}
	if _, _, err := parseResourceMountInfo([]byte(line+line), "/tmp/test space"); err == nil {
		t.Fatal("stacked mounts accepted as one identity")
	}
	stacked := line + strings.Replace(line, "10 1", "11 1", 1)
	candidates, err := parseResourceMountCandidates([]byte(stacked), "/tmp/test space")
	if err != nil || len(candidates) != 2 {
		t.Fatalf("stacked mount candidates: %v %v", candidates, err)
	}
	if id, err := selectResourceMountID(candidates, 11); err != nil || id != 11 {
		t.Fatalf("active mount selection: %d %v", id, err)
	}
	if _, err := selectResourceMountID(candidates, 12); err == nil {
		t.Fatal("mount id outside the requested path accepted")
	}
	if _, _, err := parseResourceMountInfo([]byte("incomplete"), "/tmp/test space"); err == nil {
		t.Fatal("incomplete inventory accepted")
	}
}
