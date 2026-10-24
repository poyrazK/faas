// adr: 20261024135625 — only journal writes that name a reboot-surviving
// asset fsync the directory; every write still fsyncs the record file.

package fcvm

import (
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestResourceJournalCommitDurability(t *testing.T) {
	j := openTestResourceJournal(t, t.TempDir())
	var got []bool
	j.commitSync = func(durable bool) error {
		got = append(got, durable)
		return nil
	}
	l := journalTestLease(idLive, 0)
	id := resourceFileIdentity{Device: 1, Inode: 2}
	mnt := &resourceMountIdentity{BootID: idLive, Namespace: 1, MountID: 7}
	ns := &resourceMountIdentity{BootID: idLive, Namespace: 1}
	bind := func(name string, mode uint32, readOnly bool) resourceAsset {
		return resourceAsset{Kind: "bind", Path: filepath.Join("/jail", name), Source: filepath.Join("/src", name),
			SourceFile: &id, OriginalMode: mode, ReadOnly: readOnly, Namespace: ns}
	}
	steps := []struct {
		name    string
		do      func() error
		durable bool
	}{
		{"lease intent", func() error { return j.begin(l) }, false},
		{"jail intent", func() error {
			return j.addAsset(idLive, resourceAsset{Kind: "jail", Path: "/jail/" + idLive, Namespace: ns})
		}, true},
		{"jail checkpoint", func() error { return j.checkpointAsset(idLive, "/jail/"+idLive, id, nil) }, true},
		{"bind of an already readable image", func() error { return j.addAsset(idLive, bind("vmlinux", 0o664, true)) }, false},
		{"bind target", func() error { return j.checkpointBindTarget(idLive, "/jail/vmlinux", id) }, false},
		{"bind mount", func() error { return j.checkpointAsset(idLive, "/jail/vmlinux", id, mnt) }, false},
		{"bind that widens a private source", func() error { return j.addAsset(idLive, bind("mem", 0o600, true)) }, true},
		{"writable bind of a private clone", func() error { return j.addAsset(idLive, bind("layer", 0o600, false)) }, true},
		{"clone intent", func() error { return j.addAsset(idLive, resourceAsset{Kind: "clone", Path: "/fc/clone"}) }, true},
		{"clone checkpoint", func() error { return j.checkpointAsset(idLive, "/fc/clone", id, nil) }, true},
		{"process", func() error {
			return j.recordProcess(l, resourceProcessIdentity{BootID: idLive, PID: 42, StartTicks: 9})
		}, false},
		{"retirement", func() error { return j.retireAsset(idLive, "/jail/vmlinux") }, true},
		{"record removal", func() error { return j.forget(l) }, true},
	}
	for _, s := range steps {
		got = got[:0]
		if err := s.do(); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if len(got) != 1 || got[0] != s.durable {
			t.Errorf("%s: commits %v, want one with durable=%v", s.name, got, s.durable)
		}
	}
}

// Writes added after the ADR was drafted run off the wake path and keep the
// full protocol: the epoch claim and the adoption block.
func TestResourceJournalOffWakePathWritesAreDurable(t *testing.T) {
	f := newAdoptionFixture(t)
	var got []bool
	f.journal.commitSync = func(durable bool) error {
		got = append(got, durable)
		return nil
	}
	steps := []struct {
		name string
		do   func() error
	}{
		{"epoch claim", func() error {
			return f.journal.WriteEpoch(JournalEpoch{NodeID: uuid.NewString(), Epoch: 1, BootID: uuid.NewString(),
				Incarnation: uuid.NewString(), State: JournalEpochClaiming})
		}},
		{"adoption block", func() error { return f.journal.recordAdoption(f.lease, f.process, f.block()) }},
	}
	for _, s := range steps {
		got = got[:0]
		if err := s.do(); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if len(got) != 1 || !got[0] {
			t.Errorf("%s: commits %v, want one durable commit", s.name, got)
		}
	}
}

// A volatile write is still the record's committed content: a vmmd that
// restarts on the same boot reads it back (the page cache survives a daemon
// crash), and a failed commit of either kind is reported to the caller.
func TestResourceJournalVolatileWriteReopens(t *testing.T) {
	path := t.TempDir()
	j := openTestResourceJournal(t, path)
	l := journalTestLease(idLive, 0)
	if err := j.begin(l); err != nil {
		t.Fatal(err)
	}
	if err := j.recordProcess(l, resourceProcessIdentity{BootID: idLive, PID: 42, StartTicks: 9}); err != nil {
		t.Fatal(err)
	}
	_ = j.Close()
	j = openTestResourceJournal(t, path)
	r, ok, err := j.lookup(idLive)
	if err != nil || !ok || r.Process == nil || r.Process.PID != 42 {
		t.Fatalf("reopened record = %+v, %v, %v; want the volatile process checkpoint", r, ok, err)
	}
}
