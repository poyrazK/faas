// adr: 20261026113920 — volatile journal commits go only to a tmpfs copy that
// a restart on the same boot reads; durable commits keep the disk protocol.

package fcvm

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const (
	volatileTestBoot  = "cccccccc-3333-4333-8333-cccccccccccc"
	volatileTestBoot2 = "dddddddd-4444-4444-8444-dddddddddddd"
)

// useVolatileTestHost makes every directory tmpfs and the kernel boot id
// boot. Not parallel: it replaces package hooks.
func useVolatileTestHost(t *testing.T, boot *string) {
	t.Helper()
	oldBoot, oldTmpfs := resourceVolatileBootID, resourceVolatileTmpfs
	resourceVolatileBootID = func() (string, error) { return *boot, nil }
	resourceVolatileTmpfs = func(string) (bool, error) { return true, nil }
	t.Cleanup(func() { resourceVolatileBootID, resourceVolatileTmpfs = oldBoot, oldTmpfs })
}

type volatileJournalFixture struct {
	dir, volatile string
	boot          string
}

func newVolatileJournalFixture(t *testing.T) *volatileJournalFixture {
	t.Helper()
	f := &volatileJournalFixture{dir: filepath.Join(t.TempDir(), "journal"), volatile: filepath.Join(t.TempDir(), "volatile"), boot: volatileTestBoot}
	useVolatileTestHost(t, &f.boot)
	return f
}

func (f *volatileJournalFixture) open(t *testing.T, writes bool) *ResourceJournal {
	t.Helper()
	j, err := OpenResourceJournalConfig(ResourceJournalConfig{Dir: f.dir, VolatileDir: f.volatile, VolatileWrites: writes})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	return j
}

func (f *volatileJournalFixture) diskRecord(t *testing.T, instance string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.dir, resourceRecordName(instance)))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (f *volatileJournalFixture) copies(t *testing.T) []string {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(f.volatile, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	return names
}

// recordLiveGuest writes a lease, a jail (durable) and a process (volatile).
func recordLiveGuest(t *testing.T, j *ResourceJournal) Lease {
	t.Helper()
	l := journalTestLease(idLive, 0)
	ns := &resourceMountIdentity{BootID: idLive, Namespace: 1}
	if err := j.begin(l); err != nil {
		t.Fatal(err)
	}
	if err := j.addAsset(idLive, resourceAsset{Kind: "jail", Path: "/jail/" + idLive, Namespace: ns}); err != nil {
		t.Fatal(err)
	}
	if err := j.recordProcess(l, resourceProcessIdentity{BootID: idLive, PID: 42, StartTicks: 9}); err != nil {
		t.Fatal(err)
	}
	return l
}

func TestVolatileJournalCommitsSkipTheDisk(t *testing.T) {
	f := newVolatileJournalFixture(t)
	j := f.open(t, true)
	var syncs []bool
	j.commitSync = func(durable bool) error {
		syncs = append(syncs, durable)
		return nil
	}
	l := journalTestLease(idLive, 0)
	if err := j.begin(l); err != nil {
		t.Fatal(err)
	}
	if len(syncs) != 0 || f.diskRecord(t, idLive) != nil || len(f.copies(t)) != 1 {
		t.Fatalf("lease intent: syncs %v, on disk %v, copies %d; want only a tmpfs copy", syncs, f.diskRecord(t, idLive) != nil, len(f.copies(t)))
	}
	if err := j.addAsset(idLive, resourceAsset{Kind: "jail", Path: "/jail/" + idLive}); err != nil {
		t.Fatal(err)
	}
	durable := f.diskRecord(t, idLive)
	if len(syncs) != 1 || !syncs[0] || !bytes.Contains(durable, []byte(`"jail"`)) {
		t.Fatalf("jail intent: syncs %v, disk %s; want one durable commit naming the jail", syncs, durable)
	}
	if err := j.recordProcess(l, resourceProcessIdentity{BootID: idLive, PID: 42, StartTicks: 9}); err != nil {
		t.Fatal(err)
	}
	if len(syncs) != 1 || !bytes.Equal(f.diskRecord(t, idLive), durable) {
		t.Fatalf("process checkpoint: syncs %v, disk changed %v; want the disk untouched", syncs, !bytes.Equal(f.diskRecord(t, idLive), durable))
	}
	if timing := j.takeTiming(idLive); timing.Commits != 3 {
		t.Fatalf("commits %d, want 3", timing.Commits)
	}
}

func TestVolatileJournalCopiesAreThisBootsNewestVersion(t *testing.T) {
	f := newVolatileJournalFixture(t)
	j := f.open(t, true)
	recordLiveGuest(t, j)
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}

	sameBoot := f.open(t, true)
	r, ok, err := sameBoot.lookup(idLive)
	if err != nil || !ok || r.Process == nil || len(r.Assets) != 1 {
		t.Fatalf("same boot: %+v ok=%v err=%v; want the copy with jail and process", r, ok, err)
	}
	if err := sameBoot.Close(); err != nil {
		t.Fatal(err)
	}

	f.boot = volatileTestBoot2 // a reboot empties tmpfs; a stale copy is discarded unread
	rebooted := f.open(t, true)
	r, ok, err = rebooted.lookup(idLive)
	if err != nil || !ok || r.Process != nil || len(r.Assets) != 1 {
		t.Fatalf("after reboot: %+v ok=%v err=%v; want the last durable version, jail without process", r, ok, err)
	}
	if n := len(f.copies(t)); n != 0 {
		t.Fatalf("after reboot: %d copies left, want 0", n)
	}
}

// Switching volatile writes off, or rolling back to a vmmd that ignores the
// copies after a clean restart with it off, finds the newest versions on disk.
func TestVolatileJournalSwitchOffCommitsCopiesToDisk(t *testing.T) {
	f := newVolatileJournalFixture(t)
	j := f.open(t, true)
	recordLiveGuest(t, j)
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}

	off := f.open(t, false)
	if r, ok, err := off.lookup(idLive); err != nil || !ok || r.Process == nil {
		t.Fatalf("switch off: %+v ok=%v err=%v; want the copy's process", r, ok, err)
	}
	if n := len(f.copies(t)); n != 0 {
		t.Fatalf("switch off: %d copies left, want 0", n)
	}
	if err := off.Close(); err != nil {
		t.Fatal(err)
	}

	older := openTestResourceJournal(t, f.dir)
	if r, ok, err := older.lookup(idLive); err != nil || !ok || r.Process == nil {
		t.Fatalf("disk only: %+v ok=%v err=%v; want the process on disk", r, ok, err)
	}
}

// Removal is durable on disk before the copy goes, so a restart on this boot
// never loses a record whose removal did not reach disk.
func TestVolatileJournalForgetRemovesTheDiskRecordFirst(t *testing.T) {
	f := newVolatileJournalFixture(t)
	j := f.open(t, true)
	l := recordLiveGuest(t, j)
	failing := errors.New("directory fsync failed")
	j.commitSync = func(durable bool) error {
		if durable {
			return failing
		}
		return nil
	}
	if err := j.forget(l); !errors.Is(err, failing) {
		t.Fatalf("forget = %v, want %v", err, failing)
	}
	if n := len(f.copies(t)); n != 1 {
		t.Fatalf("after an uncertain removal: %d copies, want the record's copy kept", n)
	}
	if _, ok, _ := j.lookup(idLive); !ok {
		t.Fatal("after an uncertain removal: record dropped from memory")
	}
	j.commitSync = func(bool) error { return nil }
	if err := j.forget(l); err != nil {
		t.Fatal(err)
	}
	if len(f.copies(t)) != 0 || f.diskRecord(t, idLive) != nil {
		t.Fatal("after removal: record left on disk or in tmpfs")
	}
}

func TestVolatileJournalRefusesAnotherJournalsCopies(t *testing.T) {
	f := newVolatileJournalFixture(t)
	f.open(t, true)
	other := filepath.Join(t.TempDir(), "other")
	for _, writes := range []bool{true, false} {
		if _, err := OpenResourceJournalConfig(ResourceJournalConfig{Dir: other, VolatileDir: f.volatile, VolatileWrites: writes}); err == nil {
			t.Fatalf("writes=%v: opened another journal's tmpfs copies", writes)
		}
	}
}

func TestVolatileJournalWritesNeedTmpfs(t *testing.T) {
	f := newVolatileJournalFixture(t)
	resourceVolatileTmpfs = func(string) (bool, error) { return false, nil }
	if _, err := OpenResourceJournalConfig(ResourceJournalConfig{Dir: f.dir, VolatileDir: f.volatile, VolatileWrites: true}); err == nil {
		t.Fatal("volatile writes opened on a disk filesystem")
	}
	j := f.open(t, false)
	if j.volatile != nil {
		t.Fatal("switch off opened a tmpfs directory that does not exist")
	}
	if _, err := os.Stat(f.volatile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("switch off created the tmpfs directory: %v", err)
	}
}
