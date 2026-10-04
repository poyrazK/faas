// adr: 479
package fcvm

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func preparedBootRecord(t *testing.T, source, boot, phase string) resourceJournalRecord {
	t.Helper()
	r := restartPreparedRecord(t, source, 0, boot)
	r.Version, r.Prepared.BootID = 6, boot
	switch phase {
	case "intent", "retired":
		r.Assets = nil
	case "namespace_intent":
		r.Assets = r.Assets[:1]
		r.Assets[0].File, r.Assets[0].Mount = nil, nil
	case "namespace_checkpoint":
		r.Assets = r.Assets[:1]
	case "link_intent":
		r.Assets[1].Link.Index = 0
	case "complete":
	default:
		t.Fatal("unknown prepared record phase", phase)
	}
	if err := r.validate(); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestResourcePreparedBootCommittedBeforeSetup(t *testing.T) {
	m, p, j, f := preparedJournalFixture(t)
	syncDir := j.directorySync
	committed := false
	j.directorySync = func() error {
		for _, r := range j.records {
			if len(r.Assets) == 0 {
				body, err := j.dir.ReadFile(resourceRecordName(r.Prepared.Source))
				var disk resourceJournalRecord
				if err != nil || json.Unmarshal(body, &disk) != nil || disk.Version != 6 || disk.Prepared == nil || disk.Prepared.BootID != idLive || len(f.bindings)+len(f.links) != 0 {
					t.Fatal("creator boot not persisted before physical setup", err)
				}
				committed = true
			}
		}
		return syncDir()
	}
	f.before = func(argv []string) {
		if len(argv) > 2 && argv[2] == "add" && !committed {
			t.Fatal("physical creation preceded initial boot commit")
		}
	}
	fillTestPreparedPool(t, m, p, 100)
	if !committed || len(p.ready) != 1 {
		t.Fatal("prepared boot commit missing")
	}
}

func TestResourcePreparedBootCaptureFailurePreventsCreation(t *testing.T) {
	for _, failure := range []string{"read_error", "nil_context", "missing_boot", "invalid_boot", "noncanonical_boot", "namespace_changed", "link_changed"} {
		t.Run(failure, func(t *testing.T) {
			m, p, j, f := preparedJournalFixture(t)
			calls := 0
			m.namespaceContext = func() (*resourceMountIdentity, error) {
				calls++
				creator := &resourceMountIdentity{BootID: idLive, Namespace: 1}
				switch failure {
				case "read_error":
					return nil, errors.New("boot read unavailable")
				case "nil_context":
					return nil, nil
				case "missing_boot":
					creator.BootID = ""
				case "invalid_boot":
					creator.BootID = "unknown"
				case "noncanonical_boot":
					creator.BootID = strings.ToUpper(idLive)
				case "namespace_changed":
					if calls > 1 {
						creator.BootID = idOther
					}
				}
				return creator, nil
			}
			if failure == "link_changed" {
				m.linkContext = func() (*resourceMountIdentity, error) {
					return &resourceMountIdentity{BootID: idOther, Namespace: 2}, nil
				}
			}
			f.before = func(argv []string) {
				if len(argv) > 2 && argv[2] == "add" && (failure != "link_changed" || argv[1] != "netns") {
					t.Fatal("physical creation despite unknown/changed boot")
				}
			}
			fillTestPreparedPool(t, m, p, 100)
			items, err := j.snapshot()
			if err != nil || len(p.ready)+len(p.retired)+len(m.alloc.reserved)+len(items)+len(f.bindings)+len(f.links) != 0 {
				t.Fatal("failed boot capture created resources or retained confirmed-empty reservation", err)
			}
		})
	}
}

func TestResourcePreparedBootRejectsContradictoryRecords(t *testing.T) {
	for _, failure := range []string{"missing", "invalid", "noncanonical", "legacy_boot", "no_prepared", "namespace_boot", "mount_boot", "link_boot", "guest_process_boot"} {
		t.Run(failure, func(t *testing.T) {
			path := t.TempDir()
			j := openTestResourceJournal(t, path)
			r := preparedBootRecord(t, "prepared-"+idOther, idLive, "complete")
			if err := j.beginRecord(r); err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "missing":
				r.Prepared.BootID = ""
			case "invalid":
				r.Prepared.BootID = "unknown"
			case "noncanonical":
				r.Prepared.BootID = strings.ToUpper(idLive)
			case "legacy_boot":
				r.Version = 5
			case "no_prepared", "guest_process_boot":
				r.Prepared.Target = idLive
				r.Lease = journalTestLease(idLive, 0)
				r.Assets[0].Path = filepath.Join("/run/netns", r.Lease.Netns)
				if failure == "no_prepared" {
					r.Prepared = nil
				} else {
					r.Process = &resourceProcessIdentity{BootID: idOther, PID: 100, StartTicks: 1}
				}
			case "namespace_boot":
				r.Assets[0].Namespace.BootID, r.Assets[0].Mount.BootID = idOther, idOther
			case "mount_boot":
				r.Assets[0].Mount.BootID = idOther
			case "link_boot":
				r.Assets[1].Namespace.BootID = idOther
			}
			body, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if err := j.dir.WriteFile(resourceRecordName("prepared-"+idOther), body, 0o600); err != nil {
				t.Fatal(err)
			}
			_ = j.Close()
			fresh, err := OpenResourceJournal(path)
			if fresh != nil {
				_ = fresh.Close()
			}
			if err == nil {
				t.Fatal("contradictory creator boot opened")
			}
		})
	}
}

func TestResourceRestartPreparedBootPartialRetirement(t *testing.T) {
	for _, phase := range []string{"intent", "namespace_intent", "namespace_checkpoint", "link_intent", "complete", "retired"} {
		for _, state := range []string{"prior_boot", "same_boot", "transfer", "guest", "fsync", "collision"} {
			t.Run(phase+"/"+state, func(t *testing.T) {
				opts := restartFixture(t)
				path := t.TempDir()
				j := openTestResourceJournal(t, path)
				boot := idOther
				if state == "same_boot" {
					boot = idLive
				}
				r := preparedBootRecord(t, "prepared-"+idOther, boot, phase)
				if state == "transfer" || state == "guest" {
					r.Prepared.Target = idLive
					if state == "guest" {
						r.Lease = journalTestLease(idLive, 0)
						for i := range r.Assets {
							if r.Assets[i].Kind == "netns" {
								r.Assets[i].Path = filepath.Join("/run/netns", r.Lease.Netns)
							}
						}
					}
				}
				if err := j.beginRecord(r); err != nil {
					t.Fatal(err)
				}
				_ = j.Close()
				j = openTestResourceJournal(t, path)
				m := newTestManager(&fakeRunner{}, &fakeVMM{})
				if err := m.WithResourceJournal(j); err != nil {
					t.Fatal(err)
				}
				syncDir := j.directorySync
				j.directorySync = func() error {
					if !m.alloc.pristine() || m.restartInventoryDone {
						t.Fatal("admission preceded retirement fsync")
					}
					if state == "fsync" {
						return errors.New("retirement sync unavailable")
					}
					return syncDir()
				}
				if state == "collision" {
					restartProcessFixture(t, opts, 101, 0, "unrelated")
				}
				rep, err := m.recoverRestartQuarantine(t.Context(), opts)
				if state == "fsync" {
					items, readErr := j.snapshot()
					if err == nil || !m.alloc.pristine() || m.restartInventoryDone || readErr != nil || len(items) != 1 {
						t.Fatal("uncertain retirement opened admission or lost reservation")
					}
					j.directorySync = syncDir
					rep, err = m.recoverRestartQuarantine(t.Context(), opts)
				}
				want := 0
				if state == "prior_boot" || state == "fsync" {
					want = 1
				}
				if err != nil || rep.ReclaimedPreparedRecords != want || rep.JournalRecords != 1-want || rep.Slots != 1-want || m.LeasedCount() != 0 || m.HasInstanceOwnership(r.Lease.Instance) {
					t.Fatal("partial boot recovery", rep, err)
				}
				_, readErr := j.dir.ReadFile(resourceRecordName(r.Prepared.Source))
				if (want == 1 && !errors.Is(readErr, os.ErrNotExist)) || (want == 0 && readErr != nil) {
					t.Fatal("unexpected durable reservation", readErr)
				}
				l, err := m.alloc.reserveNetwork("fresh")
				if err != nil || l.Slot != 1-want {
					t.Fatal("incorrect reservation reuse", l, err)
				}
			})
		}
	}
}
