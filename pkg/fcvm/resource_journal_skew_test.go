package fcvm

// adr: 20261017 vmmd restart adoption

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// release272Record is the journal record the previous release
// (v0.1.18-rc.272) decodes, with its unknown-field refusal. Its nested
// types are unchanged since; only the version-7 adoption block was added.
type release272Record struct {
	Version  int             `json:"version"`
	Lease    json.RawMessage `json:"lease"`
	Process  json.RawMessage `json:"process,omitempty"`
	Assets   json.RawMessage `json:"assets,omitempty"`
	Prepared json.RawMessage `json:"prepared,omitempty"`
}

// assertRelease272ReadsJournal applies the previous release's load rules:
// it refuses any entry other than records, .lock and pending temporaries,
// a field it does not know, and a version above 6, and vmmd then fails to
// start (docs/release-compatibility.md, N±1).
func assertRelease272ReadsJournal(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	records := 0
	for _, e := range entries {
		name := e.Name()
		if name == ".lock" || strings.HasPrefix(name, ".pending-") {
			continue
		}
		if !e.Type().IsRegular() || !strings.HasSuffix(name, ".json") {
			t.Fatalf("the previous release refuses journal entry %q", name)
		}
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		var r release272Record
		d := json.NewDecoder(f)
		d.DisallowUnknownFields()
		err = d.Decode(&r)
		_ = f.Close()
		if err != nil || r.Version < 1 || r.Version > 6 {
			t.Fatalf("the previous release refuses record %s: version %d, %v", name, r.Version, err)
		}
		records++
	}
	if records == 0 {
		t.Fatal("no journal record was written")
	}
}

// Until adoption_record is on, this release writes only what the previous
// one reads, so vmmd can be rolled back on a node with live guests. Admitted
// guests take the receipt commit path, which is a no-op without records.
func TestJournalWithoutAdoptionRecordsIsReadableByThePreviousRelease(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	dir := t.TempDir()
	if err := m.WithResourceJournal(openTestResourceJournal(t, dir)); err != nil {
		t.Fatal(err)
	}
	installNamespaceFixture(m)
	if _, err := m.Wake(t.Context(), wakeReq(idLive, nil)); err != nil {
		t.Fatal(err)
	}
	req := admittedFixture(t, m)
	if _, _, err := m.WakeAdmitted(t.Context(), req, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, id := range []string{idLive, req.Binding.InstanceID} {
			if err := m.Destroy(context.WithoutCancel(t.Context()), id); err != nil {
				t.Errorf("cleanup %s: %v", id, err)
			}
		}
	})
	m.FlushAdoptionRecords()
	if _, err := os.Stat(filepath.Join(dir, resourceEpochFile)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("an ownership epoch was written without adoption: %v", err)
	}
	assertRelease272ReadsJournal(t, dir)
}
