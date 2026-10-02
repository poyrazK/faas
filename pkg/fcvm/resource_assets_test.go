// adr: 400
package fcvm

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestResourceAssetsBindModeRetryAndAliases(t *testing.T) {
	v := NewJailerVMM(t.TempDir(), time.Second)
	path := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(path, []byte("owned"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	identity, _ := resourceFileID(info)
	v.bindSourceModes[path] = bindSourceMode{file: identity, mode: 0o600, refs: 1}
	held := path + ".held"
	_ = os.Rename(path, held)
	_ = os.WriteFile(path, []byte("foreign"), 0o644)
	if err := v.releaseBindSource(path); err == nil || len(v.bindSourceModes) != 1 {
		t.Fatal("failed restoration forgot source ownership")
	}
	_ = os.Remove(path)
	_ = os.Rename(held, path)
	alias := path + ".alias"
	if err := os.Link(path, alias); err != nil {
		t.Fatal(err)
	}
	v.bindSourceModes[alias] = bindSourceMode{file: identity, mode: 0o600, refs: 1}
	if err := v.releaseBindSource(path); err != nil {
		t.Fatal(err)
	}
	info, _ = os.Stat(alias)
	if info.Mode().Perm() != 0o644 {
		t.Fatal("permissions restored while alias still referenced")
	}
	if err := v.releaseBindSource(alias); err != nil {
		t.Fatal(err)
	}
	info, _ = os.Stat(alias)
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
	foreign := filepath.Join(t.TempDir(), "foreign-target")
	ns := resourceMountIdentity{BootID: idLive, Namespace: 3}
	if err := j.addAsset(idOther, resourceAsset{Kind: "bind", Path: foreign, Source: path, SourceFile: &identity, Namespace: &ns, OriginalMode: 0o600}); err != nil {
		t.Fatal(err)
	}
	v.bindSourceModes[path] = bindSourceMode{file: identity, mode: 0o600, refs: 1}
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
	if _, _, err := parseResourceMountInfo([]byte("incomplete"), "/tmp/test space"); err == nil {
		t.Fatal("incomplete inventory accepted")
	}
}
