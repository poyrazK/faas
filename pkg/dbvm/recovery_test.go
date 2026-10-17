// adr: 1027 — a jail's record round-trips and refuses what it cannot adopt.
package dbvm

import (
	"testing"
	"time"
)

func TestVMRecordRoundTripsAndValidates(t *testing.T) {
	dir := t.TempDir()
	rec := vmRecord{DatabaseID: testID, Slot: 7, Major: 17, Base: "/srv/db/bases/17-abc.ext4", Volume: "/srv/db/volumes/x.img",
		RestoreWindowSeconds: 604800, PrimaryNode: "db-2", PrimaryTarget: "10.42.0.6:9443", StandbyNode: "db-3.faas", BootedAt: time.Unix(1700000000, 0).UTC()}
	if err := writeRecord(dir, rec); err != nil {
		t.Fatal(err)
	}
	got, err := readRecord(dir)
	rec.Version = vmRecordVersion
	if err != nil || got != rec {
		t.Fatalf("read %+v, %v; want %+v", got, err, rec)
	}
	for name, bad := range map[string]func(*vmRecord){
		"no database":        func(r *vmRecord) { r.DatabaseID = "" },
		"slot":               func(r *vmRecord) { r.Slot = -1 },
		"major":              func(r *vmRecord) { r.Major = 9 },
		"half a primary":     func(r *vmRecord) { r.PrimaryTarget = "" },
		"no volume":          func(r *vmRecord) { r.Volume = "" },
		"bad restore source": func(r *vmRecord) { r.RestoreSource = "../x" },
	} {
		r := rec
		bad(&r)
		if err := writeRecord(dir, r); err == nil {
			t.Errorf("%s: wrote an invalid record", name)
		}
	}
	spare := vmRecord{DatabaseID: testID, Spare: true, Slot: 1, Major: 17}
	if err := writeRecord(dir, spare); err != nil {
		t.Fatalf("a spare's record needs no volume: %v", err)
	}
	if _, err := readRecord(t.TempDir()); err == nil {
		t.Fatal("read a record that does not exist")
	}
}

// A running VM is stale on another base than its major's current one, or
// on an older launch; waiting spares on an old base stop when it changes.
func TestStaleVMsAndRetiredSpares(t *testing.T) {
	leases := &fakeLeaser{}
	rt := testRuntime(t, leases)
	current := rt.cfg.Bases[17]
	closed := make(chan struct{})
	close(closed)
	vm := func(base string, launch int) *databaseVM {
		return &databaseVM{base: base, exited: closed, dir: t.TempDir(),
			record: vmRecord{DatabaseID: testID, Major: 17, LaunchVersion: launch}}
	}
	for name, c := range map[string]struct {
		vm   *databaseVM
		want bool
	}{
		"current":    {vm(current, launchVersion), false},
		"old base":   {vm("/srv/db/bases/17-old.ext4", launchVersion), true},
		"old launch": {vm(current, launchVersion-1), true},
	} {
		rt.mu.Lock()
		rt.vms[testID] = c.vm
		rt.mu.Unlock()
		if got := rt.Stale(testID); got != c.want {
			t.Errorf("%s: stale = %v, want %v", name, got, c.want)
		}
	}
	if rt.Stale("2b7d3c5e-8f43-4c7a-9d0e-5a1f2b3c4d5e") {
		t.Error("an unknown VM is stale")
	}

	const spareID = "6f1e2d3c-4b5a-4968-8776-5a4b3c2d1e0f"
	spare := &databaseVM{base: current, exited: closed, dir: t.TempDir(), waiting: true, spare: SpareShape{Major: 17, VcpuCount: 1, MemSizeMiB: 384},
		record: vmRecord{DatabaseID: spareID, Spare: true, Major: 17, LaunchVersion: launchVersion}}
	rt.mu.Lock()
	rt.vms = map[string]*databaseVM{spareID: spare}
	rt.mu.Unlock()
	if rt.Stale(spareID) {
		t.Fatal("a waiting spare is stale")
	}
	rt.SetBase(17, current)
	if rt.waitingSpares(spare.spare) != 1 {
		t.Fatal("setting the same base retired a spare")
	}
	rt.SetBase(17, "/srv/db/bases/17-new.ext4")
	deadline := time.Now().Add(5 * time.Second)
	for {
		rt.mu.Lock()
		_, left := rt.vms[spareID]
		rt.mu.Unlock()
		if !left {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a spare on the old base kept waiting")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
