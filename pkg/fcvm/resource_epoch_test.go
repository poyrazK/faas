package fcvm

// adr: 20261017 vmmd restart adoption

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestResourceJournalEpoch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal")
	j := openTestResourceJournal(t, path)
	if _, ok, err := j.ReadEpoch(); err != nil || ok {
		t.Fatalf("fresh journal epoch: %v %v", ok, err)
	}
	e := JournalEpoch{NodeID: uuid.NewString(), Epoch: 3, BootID: uuid.NewString(), Incarnation: uuid.NewString(), State: JournalEpochClaiming}
	if err := j.WriteEpoch(e); err != nil {
		t.Fatal(err)
	}
	e.State = JournalEpochClaimed
	if err := j.WriteEpoch(e); err != nil {
		t.Fatal(err)
	}
	// Reopening keeps the claim, and the epoch file is not taken for a lease
	// record.
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	j = openTestResourceJournal(t, path)
	if got, ok, err := j.ReadEpoch(); err != nil || !ok || got != e {
		t.Fatalf("reopened epoch = %+v %v %v, want %+v", got, ok, err, e)
	}
	for name, bad := range map[string]JournalEpoch{
		"zero epoch": {NodeID: e.NodeID, BootID: e.BootID, Incarnation: e.Incarnation, State: JournalEpochClaimed},
		"bad state":  {NodeID: e.NodeID, Epoch: 1, BootID: e.BootID, Incarnation: e.Incarnation, State: "pending"},
		"nil node":   {NodeID: uuid.Nil.String(), Epoch: 1, BootID: e.BootID, Incarnation: e.Incarnation, State: JournalEpochClaimed},
		"not a uuid": {NodeID: "fsn-4", Epoch: 1, BootID: e.BootID, Incarnation: e.Incarnation, State: JournalEpochClaimed},
	} {
		if err := j.WriteEpoch(bad); err == nil {
			t.Errorf("%s: written", name)
		}
	}
	// A tampered file is refused rather than read.
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, resourceEpochFile), []byte(`{"node_id":"x","extra":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	j = openTestResourceJournal(t, path)
	if _, _, err := j.ReadEpoch(); err == nil {
		t.Fatal("tampered epoch file read")
	}
}
