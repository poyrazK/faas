// adr: 401
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

	"github.com/onebox-faas/faas/pkg/netns"
)

type namespaceFixture struct {
	mu       sync.Mutex
	next     Runner
	bindings map[string]resourceAsset
	sequence uint64
	before   func([]string)
}

func installNamespaceFixture(m *Manager) *namespaceFixture {
	f := &namespaceFixture{next: m.run, bindings: make(map[string]resourceAsset), sequence: 10}
	m.run = f
	m.namespaceContext = func() (*resourceMountIdentity, error) {
		return &resourceMountIdentity{BootID: idLive, Namespace: 1}, nil
	}
	m.namespaceProbe = func(name string) (*resourceAsset, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		a, ok := f.bindings[name]
		if !ok {
			return nil, nil
		}
		return &cloneResourceAssets([]resourceAsset{a})[0], nil
	}
	return f
}

func (f *namespaceFixture) Run(ctx context.Context, argv []string) error {
	if f.before != nil {
		f.before(argv)
	}
	if err := f.next.Run(ctx, argv); err != nil {
		return err
	}
	if len(argv) != 4 || argv[0] != "ip" || argv[1] != "netns" {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch argv[2] {
	case "add":
		f.sequence++
		f.bindings[argv[3]] = resourceAsset{Kind: "netns", Path: filepath.Join("/run/netns", argv[3]), File: &resourceFileIdentity{Device: 4, Inode: f.sequence}, Namespace: &resourceMountIdentity{BootID: idLive, Namespace: 1}, Mount: &resourceMountIdentity{BootID: idLive, Namespace: 1, MountID: f.sequence}}
	case "del":
		delete(f.bindings, argv[3])
	}
	return nil
}

func TestResourcePlacementJailCheckpointReopenAndReplacement(t *testing.T) {
	v, j := assetFixture(t)
	syncDir := j.directorySync
	j.directorySync = func() error {
		for _, a := range j.records[idLive].Assets {
			if a.File == nil {
				if _, err := os.Lstat(a.Path); !os.IsNotExist(err) {
					t.Fatal("jail directory created before intent")
				}
			} else if filepath.Base(a.Path) == "root" {
				entries, err := os.ReadDir(a.Path)
				if err != nil || len(entries) != 0 {
					t.Fatal("jail contents staged before checkpoint")
				}
			}
		}
		return syncDir()
	}
	root, err := v.mkChroot(idLive)
	if err != nil {
		t.Fatal(err)
	}
	r, _, _ := j.lookup(idLive)
	if r.Version != 3 || len(r.Assets) != 2 || r.Assets[0].File == nil || r.Assets[1].File == nil {
		t.Fatalf("incomplete jail checkpoint: %+v", r)
	}
	j.directorySync = syncDir
	path := j.dir.Name()
	_ = j.Close()
	j = openTestResourceJournal(t, path)
	v.SetResourceJournal(j)
	fresh := NewJailerVMM(v.chrootBase, v.destroyWait)
	fresh.SetResourceJournal(j)
	if err := fresh.removeOwnedJail(idLive); err == nil {
		t.Fatal("reopened journal granted jail cleanup ownership")
	}
	for _, target := range []string{filepath.Dir(root), root} {
		held := target + ".held"
		if err := os.Rename(target, held); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(target, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := v.removeOwnedJail(idLive); err == nil {
			t.Fatal("replaced jail removed")
		}
		if _, err := os.Stat(target); err != nil {
			t.Fatal("foreign jail mutated")
		}
		_ = os.Remove(target)
		if err := os.Rename(held, target); err != nil {
			t.Fatal(err)
		}
	}
	if err := v.removeOwnedJail(idLive); err != nil {
		t.Fatal(err)
	}
	r, _, _ = j.lookup(idLive)
	if len(r.Assets) != 0 || len(v.jailAssets(idLive)) != 0 {
		t.Fatal("confirmed removal did not retire jail identities")
	}
}

func TestResourcePlacementLegacyAndInvalidRecords(t *testing.T) {
	for _, name := range []string{"legacy1", "legacy2", "old_jail", "wrong_jail", "wrong_netns", "partial_netns", "networkless"} {
		t.Run(name, func(t *testing.T) {
			path := t.TempDir()
			j := openTestResourceJournal(t, path)
			l := journalTestLease(idLive, 0)
			r := resourceJournalRecord{Version: 3, Lease: l}
			ns := resourceMountIdentity{BootID: idLive, Namespace: 1}
			r.Assets = []resourceAsset{{Kind: "netns", Path: filepath.Join("/run/netns", l.Netns), Namespace: &ns}}
			switch name {
			case "legacy1":
				r.Version, r.Assets = 1, nil
			case "legacy2":
				r.Version, r.Assets = 2, nil
			case "old_jail":
				r.Version, r.Assets = 2, []resourceAsset{{Kind: "jail", Path: filepath.Join("/jails", idLive)}}
			case "wrong_jail":
				r.Assets = []resourceAsset{{Kind: "jail", Path: "/jails/foreign"}}
			case "wrong_netns":
				r.Assets[0].Path = "/run/netns/foreign"
			case "partial_netns":
				r.Assets[0].File = &resourceFileIdentity{Inode: 1}
			case "networkless":
				r.Lease.Networkless = true
			}
			data, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if err := j.dir.WriteFile(resourceRecordName(idLive), data, 0o600); err != nil {
				t.Fatal(err)
			}
			_ = j.Close()
			reopened, err := OpenResourceJournal(path)
			if reopened != nil {
				defer func() { _ = reopened.Close() }()
			}
			if strings.HasPrefix(name, "legacy") != (err == nil) {
				t.Fatalf("record version/identity validation: %v", err)
			}
		})
	}
}

func TestResourcePlacementJailCommitFailures(t *testing.T) {
	for _, phase := range []string{"intent", "checkpoint", "retirement"} {
		t.Run(phase, func(t *testing.T) {
			v, j := assetFixture(t)
			syncDir := j.directorySync
			injected := errors.New("placement fsync fixture")
			j.directorySync = func() error {
				a := j.records[idLive].Assets
				if len(a) > 0 && ((phase == "intent" && a[0].File == nil) || (phase == "checkpoint" && a[0].File != nil)) {
					return injected
				}
				return syncDir()
			}
			root, err := v.mkChroot(idLive)
			if phase == "retirement" {
				if err != nil {
					t.Fatal(err)
				}
				j.directorySync = func() error { return injected }
				err = v.removeOwnedJail(idLive)
				if len(v.jailAssets(idLive)) != 2 {
					t.Fatal("uncertain retirement dropped live identity")
				}
			} else if _, statErr := os.Stat(v.chrootRoot(idLive)); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatal("jail contents created after failed commit")
			}
			if !errors.Is(err, injected) {
				t.Fatalf("commit failure acknowledged: %v", err)
			}
			j.directorySync = syncDir
			if err := v.removeOwnedJail(idLive); err != nil {
				t.Fatal(err)
			}
			if root != "" {
				if _, err := os.Stat(root); !os.IsNotExist(err) {
					t.Fatal("retry retained jail")
				}
			}
		})
	}
}

func TestResourcePlacementNetworkOrderingReplacementAndRetirement(t *testing.T) {
	j := openTestResourceJournal(t, t.TempDir())
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	if err := m.WithResourceJournal(j); err != nil {
		t.Fatal(err)
	}
	f := installNamespaceFixture(m)
	f.before = func(argv []string) {
		if !strings.Contains(strings.Join(argv, " "), "netns") {
			return
		}
		r, _, _ := j.lookup(idLive)
		if len(r.Assets) != 1 {
			t.Fatalf("namespace creation before intent: %+v", r)
		}
		if len(argv) > 2 && argv[2] == "exec" && r.Assets[0].File == nil {
			t.Fatal("policy applied before namespace checkpoint")
		}
	}
	inst, err := m.Wake(t.Context(), wakeReq(idLive, nil))
	if err != nil {
		t.Fatal(err)
	}
	f.before = nil
	name := inst.Lease.Netns
	owned := f.bindings[name]
	foreign := cloneResourceAssets([]resourceAsset{owned})[0]
	foreign.Mount.MountID++
	f.bindings[name] = foreign
	if err := m.Destroy(t.Context(), idLive); err == nil || m.LeasedCount() != 1 {
		t.Fatal("changed binding did not retain lease")
	}
	if !namespaceCheckpointMatches(f.bindings[name], foreign) {
		t.Fatal("foreign binding changed")
	}
	f.bindings[name] = owned
	injected := errors.New("namespace retirement fsync fixture")
	syncDir := j.directorySync
	j.directorySync = func() error { return injected }
	if err := m.Destroy(t.Context(), idLive); !errors.Is(err, injected) || m.LeasedCount() != 1 {
		t.Fatalf("uncertain retirement released lease: %v", err)
	}
	if _, ok := m.namespaceOwner(name); !ok {
		t.Fatal("uncertain retirement lost live identity")
	}
	j.directorySync = syncDir
	if err := m.Destroy(t.Context(), idLive); err != nil || m.LeasedCount() != 0 {
		t.Fatalf("retirement retry: %v", err)
	}
}

func TestResourcePlacementPreparedTransfer(t *testing.T) {
	m, p := testPreparedPool(t, 1)
	m.preparedNetworks = nil
	j := openTestResourceJournal(t, t.TempDir())
	if err := m.WithResourceJournal(j); err != nil {
		t.Fatal(err)
	}
	m.preparedNetworks = p
	f := installNamespaceFixture(m)
	p.move = func(old, new string) error {
		a := cloneResourceAssets([]resourceAsset{f.bindings[old]})[0]
		a.Path, a.Mount.MountID = filepath.Join("/run/netns", new), a.Mount.MountID+100
		f.bindings[new] = a
		delete(f.bindings, old)
		return nil
	}
	policy := fillTestPreparedPool(t, m, p, 100)
	old := p.ready[0]
	e := p.claim(idLive, policy)
	if e == nil {
		t.Fatal("prepared claim failed")
	}
	l := journalTestLease(idLive, e.lease.Slot)
	if err := j.begin(l); err != nil {
		t.Fatal(err)
	}
	if hit, err := m.setupWakeNetwork(t.Context(), e.config, e); !hit || err != nil {
		t.Fatalf("claimed namespace checkpoint: %v", err)
	}
	r, _, _ := j.lookup(idLive)
	if len(r.Assets) != 1 || r.Assets[0].Mount == nil || r.Assets[0].Mount.MountID < 100 {
		t.Fatal("claimed alias lacks new mount identity")
	}
	if _, exists := m.namespaceOwner(old.config.Netns); exists {
		t.Fatal("old alias retained ownership after transfer")
	}
	p.discard(*e)
	if len(p.retired) != 0 || m.LeasedCount() != 0 {
		t.Fatal("prepared cleanup retained lease")
	}
	if err := j.forget(l); err != nil {
		t.Fatal(err)
	}
}

func TestResourcePlacementNetworkCheckpointFailure(t *testing.T) {
	j := openTestResourceJournal(t, t.TempDir())
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	if err := m.WithResourceJournal(j); err != nil {
		t.Fatal(err)
	}
	f := installNamespaceFixture(m)
	l := journalTestLease(idLive, 0)
	if err := j.begin(l); err != nil {
		t.Fatal(err)
	}
	syncDir := j.directorySync
	injected := errors.New("namespace checkpoint fsync fixture")
	j.directorySync = func() error {
		if len(j.records[idLive].Assets) > 0 && j.records[idLive].Assets[0].File != nil {
			return injected
		}
		return syncDir()
	}
	nc := netns.NewConfig(l.Instance, l.Netns, l.VethHost, l.VethPeer, l.HostIP)
	if err := m.setupNetwork(t.Context(), nc); !errors.Is(err, injected) {
		t.Fatalf("failed checkpoint applied policy: %v", err)
	}
	if len(f.next.(*fakeRunner).commands) != 1 {
		t.Fatal("commands ran after failed checkpoint")
	}
	j.directorySync = syncDir
	if err := m.removeNamespaceForRebuild(t.Context(), nc); err != nil {
		t.Fatal(err)
	}
}

func TestResourcePlacementAbsentPathRequiresCreatorContext(t *testing.T) {
	a := resourceAsset{Kind: "jail", Path: filepath.Join(t.TempDir(), idLive), Namespace: &resourceMountIdentity{BootID: idLive, Namespace: 1}}
	if err := checkJailAsset(a); err == nil {
		t.Fatal("jail absence in another mount namespace accepted")
	}
	j := openTestResourceJournal(t, t.TempDir())
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	if err := m.WithResourceJournal(j); err != nil {
		t.Fatal(err)
	}
	f := installNamespaceFixture(m)
	inst, err := m.Wake(t.Context(), wakeReq(idLive, nil))
	if err != nil {
		t.Fatal(err)
	}
	delete(f.bindings, inst.Lease.Netns)
	original := m.namespaceContext
	m.namespaceContext = func() (*resourceMountIdentity, error) {
		return &resourceMountIdentity{BootID: idOther, Namespace: 1}, nil
	}
	if err := m.Destroy(t.Context(), idLive); err == nil || m.LeasedCount() != 1 {
		t.Fatal("namespace absence in another boot released lease")
	}
	m.namespaceContext = original
	if err := m.Destroy(t.Context(), idLive); err != nil || m.LeasedCount() != 0 {
		t.Fatalf("creator context retry: %v", err)
	}
}
