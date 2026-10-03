// adr: 404
package fcvm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func restartPreparedRecord(t *testing.T, source string, slot int, boot string) resourceJournalRecord {
	t.Helper()
	l := leaseForSlot(source, slot)
	r := resourceJournalRecord{Version: 5, Lease: l, Prepared: &resourcePreparedNetwork{Source: source}, Assets: []resourceAsset{
		{Kind: "netns", Path: filepath.Join("/run/netns", l.Netns), Namespace: &resourceMountIdentity{BootID: boot, Namespace: 1},
			File: &resourceFileIdentity{Device: 4, Inode: 10}, Mount: &resourceMountIdentity{BootID: boot, Namespace: 1, MountID: 11}},
		{Kind: "veth", Path: resourceLinkPath(l.VethHost), Namespace: &resourceMountIdentity{BootID: boot, Namespace: 2},
			Link: &resourceLinkIdentity{Index: 12, Name: l.VethHost, Kind: "veth", Address: "02:00:00:00:00:01"}},
	}}
	if err := r.validate(); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestResourceRestartPreparedRetiresBeforeAllocation(t *testing.T) {
	path := t.TempDir()
	j := openTestResourceJournal(t, path)
	r := restartPreparedRecord(t, "prepared-"+idOther, 0, idOther)
	if err := j.beginRecord(r); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	j = openTestResourceJournal(t, path)
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)
	if err := m.WithResourceJournal(j); err != nil {
		t.Fatal(err)
	}
	syncDir := j.directorySync
	j.directorySync = func() error {
		if !m.alloc.pristine() || m.restartInventoryDone {
			t.Fatal("allocation opened before retirement fsync")
		}
		return syncDir()
	}
	rep, err := m.recoverRestartQuarantine(t.Context(), restartFixture(t))
	if err != nil || rep != (RestartQuarantineReport{ReclaimedPreparedRecords: 1}) {
		t.Fatalf("retirement: %+v, %v", rep, err)
	}
	if _, err := j.dir.ReadFile(resourceRecordName(r.Prepared.Source)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("retired record still published")
	}
	l, err := m.alloc.reserveNetwork("fresh")
	if err != nil || l.Slot != 0 || m.LeasedCount() != 0 {
		t.Fatal("retired slot unavailable or charged as VM admission")
	}
	if len(run.commands)+len(vmm.killed)+vmm.bootCount != 0 || len(m.resourceNetworks)+len(m.resourceLinks) != 0 {
		t.Fatal("journal retirement touched physical resources or reconstructed ownership")
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	j = openTestResourceJournal(t, path)
	m = newTestManager(&fakeRunner{}, &fakeVMM{})
	if err := m.WithResourceJournal(j); err != nil {
		t.Fatal(err)
	}
	rep, err = m.recoverRestartQuarantine(t.Context(), restartFixture(t))
	if err != nil || rep != (RestartQuarantineReport{}) || !m.alloc.pristine() {
		t.Fatalf("retirement replay: %+v, %v", rep, err)
	}
}

func TestResourceRestartPreparedKeepsAmbiguousReservations(t *testing.T) {
	for _, phase := range []string{"same_boot", "transfer", "guest", "no_assets", "namespace_intent", "namespace_checkpoint", "link_intent", "mixed_boots"} {
		t.Run(phase, func(t *testing.T) {
			opts := restartFixture(t)
			j := openTestResourceJournal(t, t.TempDir())
			r := restartPreparedRecord(t, "prepared-"+idOther, 0, idOther)
			ids := 1
			switch phase {
			case "same_boot":
				r = restartPreparedRecord(t, r.Lease.Instance, 0, idLive)
			case "transfer", "guest":
				r.Prepared.Target, ids = idLive, 2
				if phase == "guest" {
					r.Lease = journalTestLease(idLive, 0)
					r.Assets[0].Path = filepath.Join("/run/netns", r.Lease.Netns)
				}
			case "no_assets":
				r.Assets = nil
			case "namespace_intent", "namespace_checkpoint":
				r.Assets = r.Assets[:1]
				if phase == "namespace_intent" {
					r.Assets[0].File, r.Assets[0].Mount = nil, nil
				}
			case "link_intent":
				r.Assets[1].Link.Index = 0
			case "mixed_boots":
				r.Assets[1].Namespace.BootID = idDead
			}
			if err := j.beginRecord(r); err != nil {
				t.Fatal(err)
			}
			m := newTestManager(&fakeRunner{}, &fakeVMM{})
			if err := m.WithResourceJournal(j); err != nil {
				t.Fatal(err)
			}
			rep, err := m.recoverRestartQuarantine(t.Context(), opts)
			if err != nil || rep.JournalRecords != 1 || rep.Slots != 1 || rep.Instances != ids || rep.ReclaimedPreparedRecords != 0 {
				t.Fatalf("quarantine: %+v, %v", rep, err)
			}
			if _, err := j.dir.ReadFile(resourceRecordName(r.Prepared.Source)); err != nil {
				t.Fatal("ambiguous record retired")
			}
			l, err := m.alloc.reserveNetwork("fresh")
			if err != nil || l.Slot != 1 {
				t.Fatal("ambiguous slot reused")
			}
		})
	}
}

func TestResourceRestartPreparedKeepsPhysicalCollisions(t *testing.T) {
	for _, name := range []string{"host", "peer", "private_host", "private_peer", "namespace", "dangling_namespace", "jail", "guest_process", "unrelated_uid", "partial_uid"} {
		t.Run(name, func(t *testing.T) {
			opts := restartFixture(t)
			j := openTestResourceJournal(t, t.TempDir())
			r := restartPreparedRecord(t, "prepared-"+idOther, 0, idOther)
			if err := j.beginRecord(r); err != nil {
				t.Fatal(err)
			}
			var path string
			privateHost, privatePeer := privateVethNames(0)
			switch name {
			case "host":
				path = filepath.Join(opts.netRoot, r.Lease.VethHost)
			case "peer":
				path = filepath.Join(opts.netRoot, r.Lease.VethPeer)
			case "private_host":
				path = filepath.Join(opts.netRoot, privateHost)
			case "private_peer":
				path = filepath.Join(opts.netRoot, privatePeer)
			case "namespace", "dangling_namespace":
				path = filepath.Join(opts.netnsRoot, r.Lease.Netns)
			case "jail":
				path = filepath.Join(opts.jailRoot, r.Lease.Instance)
			case "guest_process":
				restartProcessFixture(t, opts, 101, 0, "firecracker", "--id", idLive)
			case "unrelated_uid", "partial_uid":
				restartProcessFixture(t, opts, 101, 0, "unrelated")
				if name == "partial_uid" {
					if err := os.WriteFile(filepath.Join(opts.procRoot, "101/status"), []byte("Uid: 0 0 0 20000\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if path != "" {
				var err error
				if name == "dangling_namespace" {
					err = os.Symlink("absent", path)
				} else {
					err = os.WriteFile(path, []byte("foreign"), 0o600)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			m := newTestManager(&fakeRunner{}, &fakeVMM{})
			if err := m.WithResourceJournal(j); err != nil {
				t.Fatal(err)
			}
			rep, err := m.recoverRestartQuarantine(t.Context(), opts)
			if err != nil || rep.Slots != 1 || rep.JournalRecords != 1 || rep.ReclaimedPreparedRecords != 0 {
				t.Fatalf("collision inventory: %+v, %v", rep, err)
			}
			if path != "" {
				if _, err := os.Lstat(path); err != nil {
					t.Fatal("foreign resource removed", err)
				}
			}
		})
	}
}

func TestResourceRestartPreparedFailsClosed(t *testing.T) {
	for _, failure := range []string{"boot_absent", "boot_invalid", "uid_unreadable", "uid_missing", "uid_invalid", "fsync", "cancelled"} {
		t.Run(failure, func(t *testing.T) {
			opts := restartFixture(t)
			j := openTestResourceJournal(t, t.TempDir())
			r := restartPreparedRecord(t, "prepared-"+idOther, 0, idOther)
			if err := j.beginRecord(r); err != nil {
				t.Fatal(err)
			}
			m := newTestManager(&fakeRunner{}, &fakeVMM{})
			if err := m.WithResourceJournal(j); err != nil {
				t.Fatal(err)
			}
			syncDir := j.directorySync
			switch failure {
			case "boot_absent":
				if err := os.Remove(filepath.Join(opts.procRoot, "sys/kernel/random/boot_id")); err != nil {
					t.Fatal(err)
				}
			case "boot_invalid":
				if err := os.WriteFile(filepath.Join(opts.procRoot, "sys/kernel/random/boot_id"), []byte("unknown"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "uid_unreadable", "uid_missing", "uid_invalid":
				restartProcessFixture(t, opts, 101, 1, "unrelated")
				path := filepath.Join(opts.procRoot, "101/status")
				if failure == "uid_unreadable" {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
				} else {
					data := "Name: unrelated\n"
					if failure == "uid_invalid" {
						data = "Uid: 0 0 invalid 0\n"
					}
					if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			case "fsync":
				j.directorySync = func() error { return errors.New("retirement fsync unavailable") }
			case "cancelled":
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if _, err := m.recoverRestartQuarantine(ctx, opts); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if !m.alloc.pristine() || m.restartInventoryDone {
					t.Fatal("cancelled inventory opened admission")
				}
				return
			}
			if _, err := m.recoverRestartQuarantine(t.Context(), opts); err == nil || !m.alloc.pristine() || m.restartInventoryDone {
				t.Fatal("uncertain retirement opened admission")
			}
			items, err := j.snapshot()
			if err != nil || len(items) != 1 {
				t.Fatal("uncertain retirement lost cache reservation")
			}
			if failure == "fsync" {
				j.directorySync = syncDir
				rep, err := m.recoverRestartQuarantine(t.Context(), opts)
				if err != nil || rep.ReclaimedPreparedRecords != 1 || !m.alloc.pristine() {
					t.Fatal("confirmed retirement retry failed", rep, err)
				}
			}
		})
	}
}

func TestResourceRestartPreparedCancellationAfterRetirement(t *testing.T) {
	j := openTestResourceJournal(t, t.TempDir())
	r := restartPreparedRecord(t, "prepared-"+idOther, 0, idOther)
	if err := j.beginRecord(r); err != nil {
		t.Fatal(err)
	}
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	if err := m.WithResourceJournal(j); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	syncDir := j.directorySync
	j.directorySync = func() error {
		cancel()
		return syncDir()
	}
	if _, err := m.recoverRestartQuarantine(ctx, restartFixture(t)); !errors.Is(err, context.Canceled) || !m.alloc.pristine() || m.restartInventoryDone {
		t.Fatal("cancelled recovery opened admission", err)
	}
	j.directorySync = syncDir
	rep, err := m.recoverRestartQuarantine(t.Context(), restartFixture(t))
	if err != nil || rep != (RestartQuarantineReport{}) || !m.alloc.pristine() {
		t.Fatal("confirmed retirement replay failed", rep, err)
	}
}

func TestResourceRestartPreparedRetirementRechecksRecord(t *testing.T) {
	j := openTestResourceJournal(t, t.TempDir())
	r := restartPreparedRecord(t, "prepared-"+idOther, 0, idOther)
	if err := j.beginRecord(r); err != nil {
		t.Fatal(err)
	}
	if err := j.transferPrepared(r.Lease.Instance, idLive); err != nil {
		t.Fatal(err)
	}
	if retired, err := j.forgetRestartPrepared(r.Lease, idLive); err == nil || retired {
		t.Fatal("changed transfer intent retired")
	}
	items, err := j.snapshot()
	if err != nil || len(items) != 1 || items[0].Prepared.Target != idLive {
		t.Fatal("transfer reservation lost")
	}
}

func TestResourceRestartPreparedUIDFields(t *testing.T) {
	for _, status := range []string{"Uid: 0 20000 29999 0\n", "Uid:\t20000\t20000\t20000\t20000\n"} {
		slots := make(map[int]struct{})
		if err := restartStatusUIDSlots(status, slots); err != nil {
			t.Fatal(err)
		}
		if _, ok := slots[0]; !ok {
			t.Fatal("UID reservation missing")
		}
	}
	for i, status := range []string{"", "Uid: 0 0", "Uid: -1 0 0 0", "Uid: 4294967296 0 0 0", "Uid: 0 0 0 invalid"} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			if err := restartStatusUIDSlots(status, make(map[int]struct{})); err == nil {
				t.Fatal("invalid UIDs accepted")
			}
		})
	}
}
