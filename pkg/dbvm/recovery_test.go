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
