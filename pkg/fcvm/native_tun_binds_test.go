//go:build linux || darwin

// adr: 521 — TUN effects retain original prepared VM, device and mount ownership.
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// This backend models device and mount identities. It is not native evidence.
type nativeTunMountFixture struct {
	source nativeTunSource
	id     uint64
}
type nativeTunBackendFixture struct {
	mu                 sync.Mutex
	mounts             map[string]nativeTunMountFixture
	roots              map[string]string
	prepares           int
	namespaceAvailable bool
	beforeBind         func()
	retireError        error
}

func newNativeTunBackendFixture() *nativeTunBackendFixture {
	return &nativeTunBackendFixture{mounts: make(map[string]nativeTunMountFixture), roots: make(map[string]string), namespaceAvailable: true}
}

type nativeTunPreparationFixture struct {
	backend *nativeTunBackendFixture
	root    string
}

func (*nativeTunPreparationFixture) Source() nativeTunSource {
	return nativeTunSource{Identity: nativeLoopIdentity{Device: 21, Inode: 45}, Rdev: nativeTunRdev, Mode: 0o666}
}
func (*nativeTunPreparationFixture) Namespace() nativeLoopIdentity {
	return nativeLoopIdentity{Device: 22, Inode: 46}
}
func (*nativeTunPreparationFixture) Close() error { return nil }
func (b *nativeTunBackendFixture) Prepare(owner nativeLaunchRecord, root string) (nativeTunPreparation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if prior := b.roots[root]; prior != "" && prior != owner.Generation {
		return nil, errors.New("fixture: root generation changed")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(filepath.Join(root, nativeTunTargetName)); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("fixture: target already exists")
	}
	b.prepares++
	b.roots[root] = owner.Generation
	return &nativeTunPreparationFixture{backend: b, root: root}, nil
}
func (p *nativeTunPreparationFixture) Bind(publish func(nativeLoopIdentity) error) (uint64, error) {
	if p.backend.beforeBind != nil {
		p.backend.beforeBind()
	}
	file, err := os.OpenFile(filepath.Join(p.root, nativeTunTargetName), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	if err := file.Close(); err != nil {
		return 0, err
	}
	if err := publish(nativeLoopIdentity{Device: 23, Inode: 47}); err != nil {
		return 0, err
	}
	p.backend.mu.Lock()
	p.backend.mounts[p.root] = nativeTunMountFixture{source: p.Source(), id: 125}
	p.backend.mu.Unlock()
	return 125, nil
}
func (b *nativeTunBackendFixture) check(record nativeTunBindRecord, removed bool) error {
	if !b.namespaceAvailable {
		return errors.New("fixture: original namespace unavailable")
	}
	if b.roots[record.Root] != record.Owner.Generation {
		if removed {
			return nil
		}
		return errors.New("fixture: original root unavailable")
	}
	mount, exists := b.mounts[record.Root]
	if removed {
		if exists {
			return errors.New("fixture: retired mount remains")
		}
		if _, err := os.Lstat(filepath.Join(record.Root, nativeTunTargetName)); !errors.Is(err, os.ErrNotExist) {
			return errors.New("fixture: retired placeholder remains")
		}
		return nil
	}
	if !exists || mount.source != record.Source || mount.id != record.MountID {
		return errors.New("fixture: original mount changed")
	}
	return nil
}
func (b *nativeTunBackendFixture) Check(record nativeTunBindRecord, removed bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.check(record, removed)
}
func (b *nativeTunBackendFixture) Retire(record nativeTunBindRecord) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.namespaceAvailable || b.roots[record.Root] != record.Owner.Generation {
		return errors.New("fixture: original namespace or root unavailable")
	}
	if mount, exists := b.mounts[record.Root]; exists {
		if record.Placeholder.Inode == 0 || mount.source != record.Source || record.MountID != 0 && mount.id != record.MountID {
			return errors.New("fixture: retirement mount changed")
		}
		delete(b.mounts, record.Root)
	}
	if err := os.Remove(filepath.Join(record.Root, nativeTunTargetName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return b.retireError
}
func (b *nativeTunBackendFixture) Inventory(records []nativeTunBindRecord) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	known := make(map[string]bool)
	for _, record := range records {
		if !b.namespaceAvailable {
			return errors.New("fixture: namespace unavailable")
		}
		if record.Removed {
			if err := b.check(record, true); err != nil {
				return err
			}
			continue
		}
		if b.roots[record.Root] != record.Owner.Generation {
			return errors.New("fixture: pending root unavailable")
		}
		if mount, exists := b.mounts[record.Root]; exists {
			if record.Placeholder.Inode == 0 || mount.source != record.Source || record.MountID != 0 && mount.id != record.MountID {
				return errors.New("fixture: pending mount changed")
			}
			known[record.Root] = true
		}
	}
	for root := range b.mounts {
		if !known[root] {
			return errors.New("fixture: unowned mount")
		}
	}
	return nil
}

func nativeTunJournalFixture(t *testing.T) (*nativeTunBindJournal, nativeLaunchRecord, *nativeTunBackendFixture, string) {
	t.Helper()
	ownerJournal, lease := preparedNativeJournal(t)
	owner, err := ownerJournal.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	backend := newNativeTunBackendFixture()
	ownerJournal.tunBinds = backend
	root := filepath.Join(t.TempDir(), "firecracker", lease.Instance, "root")
	return &nativeTunBindJournal{owner: ownerJournal, backend: backend}, owner, backend, root
}
func retireNativeTunOwner(t *testing.T, j *nativeTunBindJournal, owner nativeLaunchRecord) nativeLaunchRecord {
	t.Helper()
	owner, err := j.owner.revoke(t.Context(), owner.Lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.owner.confirmExit(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	owner, err = j.owner.read(owner.Lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func TestNativeTunPublicationFailuresRemainRecoverableByOriginalOwner(t *testing.T) {
	for _, failure := range []string{"plan", "placeholder", "ready", "retired"} {
		t.Run(failure, func(t *testing.T) {
			j, owner, backend, root := nativeTunJournalFixture(t)
			j.writeValue = func(path string, record nativeTunBindRecord) error {
				if failure == "plan" && record.Placeholder.Inode == 0 || failure == "placeholder" && record.Placeholder.Inode != 0 && !record.Ready || failure == "ready" && record.Ready && !record.Removed || failure == "retired" && record.Removed {
					return errors.New("injected TUN publication failure")
				}
				return writeNativeJournalValue(path, record)
			}
			err := j.stage(t.Context(), owner, root)
			if failure != "retired" && err == nil || failure == "retired" && err != nil {
				t.Fatalf("stage: %v", err)
			}
			if failure == "plan" {
				if len(backend.mounts) != 0 {
					t.Fatal("plan failure produced a mount")
				}
				return
			}
			if err := j.stage(t.Context(), owner, root); err == nil {
				t.Fatal("uncertain binding was overwritten")
			}
			if failure != "retired" {
				if ticket, err := j.owner.beginLaunch(t.Context(), owner.Lease); err == nil {
					_ = ticket.close()
					t.Fatal("unfinished binding authorized launch")
				}
			}
			owner = retireNativeTunOwner(t, j, owner)
			if err := j.owner.confirmResourcesRemoved(t.Context(), owner); err == nil {
				t.Fatal("unretired TUN frame released ownership")
			}
			if _, err := j.owner.replace(t.Context(), owner.Lease, owner.Generation, false); err == nil {
				t.Fatal("fallback borrowed unretired TUN ownership")
			}
			if failure == "retired" && j.retire(t.Context(), owner) == nil {
				t.Fatal("retirement publication failure was ignored")
			}
			restarted := &nativeTunBindJournal{owner: nativeJournalFixture(j.owner.root), backend: backend}
			restarted.owner.tunBinds = backend
			if err := restarted.inventory(t.Context(), []nativeLaunchRecord{owner}); err != nil {
				t.Fatal(err)
			}
			if err := restarted.retire(t.Context(), owner); err != nil {
				t.Fatal(err)
			}
			if err := restarted.require(owner, true); err != nil {
				t.Fatal(err)
			}
			if err := restarted.owner.confirmResourcesRemoved(t.Context(), owner); err != nil {
				t.Fatal(err)
			}
			if len(backend.mounts) != 0 {
				t.Fatal("recovery retained modeled mount")
			}
		})
	}
}

func TestNativeTunChangedMountOrNamespaceHoldsOwnership(t *testing.T) {
	for _, change := range []string{"device", "mount", "namespace", "root"} {
		t.Run(change, func(t *testing.T) {
			j, owner, backend, root := nativeTunJournalFixture(t)
			if err := j.stage(t.Context(), owner, root); err != nil {
				t.Fatal(err)
			}
			owner = retireNativeTunOwner(t, j, owner)
			mount := backend.mounts[root]
			switch change {
			case "device":
				mount.source.Identity.Inode++
				backend.mounts[root] = mount
			case "mount":
				mount.id++
				backend.mounts[root] = mount
			case "namespace":
				backend.namespaceAvailable = false
			case "root":
				backend.roots[root] = "replacement"
			}
			if err := j.inventory(t.Context(), []nativeLaunchRecord{owner}); err == nil {
				t.Fatal("startup accepted changed original authority")
			}
			if err := j.retire(t.Context(), owner); err == nil {
				t.Fatal("changed authority authorized cleanup")
			}
			if err := j.owner.confirmResourcesRemoved(t.Context(), owner); err == nil {
				t.Fatal("changed authority released ownership")
			}
			if len(backend.mounts) != 1 {
				t.Fatal("failed cleanup removed an unknown mount")
			}
		})
	}
}

func TestNativeTunRevocationWaitsForProducerAndRefusesLateCaller(t *testing.T) {
	j, owner, backend, root := nativeTunJournalFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	backend.beforeBind = func() { close(entered); <-release }
	done := make(chan error, 1)
	go func() { done <- j.stage(t.Context(), owner, root) }()
	<-entered
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	_, err := j.owner.revoke(ctx, owner.Lease.Instance)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("revocation bypassed producer: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	owner = retireNativeTunOwner(t, j, owner)
	if err := j.retire(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if err := j.owner.confirmResourcesRemoved(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	fresh, err := j.owner.replace(t.Context(), owner.Lease, owner.Generation, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.stage(t.Context(), owner, root); err == nil {
		t.Fatal("stale prepared caller borrowed a fresh generation")
	}
	if backend.prepares != 1 {
		t.Fatal("late caller reached backend")
	}
	if err := j.retire(t.Context(), fresh); err == nil {
		t.Fatal("fresh generation borrowed prior exit proof")
	}
	if _, err := j.owner.records(t.Context()); err != nil {
		t.Fatal("completed archived binding failed enumeration:", err)
	}
}

func TestNativeTunJournalRejectsDamagedAndUntrustedRecords(t *testing.T) {
	for _, damage := range []string{"missing", "null", "duplicate", "extra", "source", "owner", "trailing", "public", "symlink", "generation"} {
		t.Run(damage, func(t *testing.T) {
			j, owner, _, root := nativeTunJournalFixture(t)
			if err := j.stage(t.Context(), owner, root); err != nil {
				t.Fatal(err)
			}
			records, err := j.records()
			if err != nil {
				t.Fatal(err)
			}
			path := j.path(records[0])
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			s := string(data)
			switch damage {
			case "missing":
				s = strings.Replace(s, `"ready":true,`, "", 1)
			case "null":
				s = strings.Replace(s, `"removed":false`, `"removed":null`, 1)
			case "duplicate":
				s = strings.Replace(s, `"ready":true`, `"ready":true,"ready":false`, 1)
			case "extra":
				s = strings.Replace(s, `"rdev":2760`, `"extra":1,"rdev":2760`, 1)
			case "source":
				s = strings.Replace(s, `"rdev":2760`, `"rdev":2761`, 1)
			case "owner":
				s = strings.Replace(s, `"authorized":false,`, "", 1)
			case "trailing":
				s += "{}"
			case "public":
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("/dev/null", path); err != nil {
					t.Fatal(err)
				}
			case "generation":
				if err := os.Rename(path, filepath.Join(j.root(), "d3124d9a-657b-4f37-a9df-f5dfb2e50764.json")); err != nil {
					t.Fatal(err)
				}
			}
			if damage != "public" && damage != "symlink" && damage != "generation" {
				if s == string(data) {
					t.Fatal("damage fixture did not change record")
				}
				if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := j.records(); err == nil {
				t.Fatal("damaged record accepted")
			}
		})
	}
}

func TestNativeTunUnknownFramesAndKernelEffectsHoldStartup(t *testing.T) {
	j, owner, backend, root := nativeTunJournalFixture(t)
	if err := j.stage(t.Context(), owner, root); err != nil {
		t.Fatal(err)
	}
	if err := j.inventory(t.Context(), nil); err == nil {
		t.Fatal("binding without current or archived owner accepted")
	}
	backend.mounts[root+"-unknown"] = backend.mounts[root]
	if err := j.inventory(t.Context(), []nativeLaunchRecord{owner}); err == nil {
		t.Fatal("unowned native mount accepted")
	}
	delete(backend.mounts, root+"-unknown")
	owner.ResourcesRemoved = true
	if err := j.validateOwners(mustNativeTunRecords(t, j), []nativeLaunchRecord{owner}); err == nil {
		t.Fatal("finished owner retained an unfinished bind")
	}
	j.backend = nil
	if err := j.require(owner, false); err == nil {
		t.Fatal("missing native backend granted proof")
	}
}
func mustNativeTunRecords(t *testing.T, j *nativeTunBindJournal) []nativeTunBindRecord {
	t.Helper()
	records, err := j.records()
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func TestNativeTunBootStagingNeverUsesLegacyBindList(t *testing.T) {
	_, v, _ := nativeManagerFixture(t)
	lease := leaseForSlot("tun-original-boot", 3)
	if err := v.prepareNativeLease(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	owner, err := v.nativeDriveStagingOwner(t.Context(), lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.bindTunSourceForOwner(t.Context(), owner, v.chrootRoot(lease.Instance), lease.Instance); err != nil {
		t.Fatal(err)
	}
	if len(v.bindMounts[lease.Instance]) != 0 {
		t.Fatal("native TUN escaped to instance-only legacy list")
	}
	changed := owner
	changed.Generation = "d3124d9a-657b-4f37-a9df-f5dfb2e50764"
	if err := v.bindTunSourceForOwner(t.Context(), changed, v.chrootRoot(lease.Instance), lease.Instance); err == nil {
		t.Fatal("boot borrowed a replacement's TUN producer")
	}
	records := mustNativeTunRecords(t, &nativeTunBindJournal{owner: v.nativeRecovery.journal})
	data, err := json.Marshal(records[0])
	if err != nil || !strings.Contains(string(data), owner.Generation) {
		t.Fatal("original owner not retained:", err)
	}
}

func TestNativeTunPrivateDeviceSetupRefusesMissingOriginalAuthority(t *testing.T) {
	_, v, _ := nativeManagerFixture(t)
	if _, err := v.bindTunDeviceInJailerForOwner(t.Context(), nativeLaunchRecord{}, v.chrootRoot("unprepared-tun"), "unprepared-tun", 20000, 20000); err == nil || !strings.Contains(err.Error(), "original local lease") {
		t.Fatalf("native private-device setup bypassed original namespace handoff: %v", err)
	}
	if len(v.bindMounts) != 0 {
		t.Fatal("refused setup recorded a legacy bind")
	}
}

func TestNativeTunIncompleteFrameFencesOtherHostProducers(t *testing.T) {
	j, owner, _, root := nativeTunJournalFixture(t)
	j.writeValue = func(path string, record nativeTunBindRecord) error {
		if record.Ready {
			return errors.New("injected final publication failure")
		}
		return writeNativeJournalValue(path, record)
	}
	if err := j.stage(t.Context(), owner, root); err == nil {
		t.Fatal("fixture did not leave unfinished attachment")
	}
	loopBackend := &nativeLoopBackendFixture{attachments: make(map[string]bool)}
	j.owner.loopMounts = loopBackend
	loops := &nativeLoopMountJournal{owner: j.owner, backend: loopBackend}
	called := false
	if err := loops.session(t.Context(), owner, "/drive.img", func(string) error { called = true; return nil }); err == nil || !strings.Contains(err.Error(), "TUN bind") {
		t.Fatalf("unfinished TUN binding did not fence writer: %v", err)
	}
	if called || loopBackend.prepares != 0 {
		t.Fatal("writer reached native effects")
	}
	imageBackend := newNativeImageBackendFixture()
	j.owner.imageSources = imageBackend
	images := &nativeImageSourceJournal{owner: j.owner, backend: imageBackend}
	if _, err := images.stage(t.Context(), owner, root, "/source.img", "base", true, 0o044, true); err == nil || !strings.Contains(err.Error(), "TUN bind") {
		t.Fatalf("unfinished TUN binding did not fence image attachment: %v", err)
	}
	if imageBackend.prepares != 0 {
		t.Fatal("image producer reached native effects")
	}
	groups := newNativeHelperGroupsFixture()
	helpers := &nativeHostHelperJournal{owner: j.owner, groups: groups}
	cmd, _ := nativeHelperFixtureCommand(t)
	if _, started, err := helpers.launch(t.Context(), owner, cmd); err == nil || started || !strings.Contains(err.Error(), "TUN bind") {
		t.Fatalf("unfinished TUN binding did not fence helper: %v started=%v", err, started)
	}
	if groups.creates != 0 || cmd.Process != nil {
		t.Fatal("helper producer reached native effects")
	}
}
