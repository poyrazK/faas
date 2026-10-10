// adr: 933
package fcvm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const deadTestPID, deadTestTicks, deadTestNetns = 4242, 7, 4026530001

// deadRecordFixture journals a same-boot instance whose process is gone and
// whose kernel resources were torn down, leaving only its temporary files —
// the shape of the 11 dead records on production-us fsn-2 after rc.251.
func deadRecordFixture(t *testing.T, opts restartInventoryOptions) (resourceJournalRecord, string, string) {
	t.Helper()
	files := t.TempDir()
	materialised := filepath.Join(files, "faas-snap-TEST")
	clone := filepath.Join(files, ".faas-layer-"+idOther)
	identity := func(path string) *resourceFileIdentity {
		if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		id, err := resourceFileID(info)
		if err != nil {
			t.Fatal(err)
		}
		return &id
	}
	l := journalTestLease(idOther, 0)
	jail := filepath.Join(t.TempDir(), "jail", l.Instance)
	r := resourceJournalRecord{Version: 4, Lease: l,
		Process: &resourceProcessIdentity{BootID: idLive, PID: deadTestPID, StartTicks: deadTestTicks},
		Assets: []resourceAsset{
			{Kind: "netns", Path: filepath.Join("/run/netns", l.Netns), Namespace: &resourceMountIdentity{BootID: idLive, Namespace: 1},
				File: &resourceFileIdentity{Device: 4, Inode: deadTestNetns}, Mount: &resourceMountIdentity{BootID: idLive, Namespace: 1, MountID: 11}},
			{Kind: "veth", Path: resourceLinkPath(l.VethHost), Namespace: &resourceMountIdentity{BootID: idLive, Namespace: 2},
				Link: &resourceLinkIdentity{Index: 12, Name: l.VethHost, Kind: "veth", Address: "02:00:00:00:00:01"}},
			{Kind: "jail", Path: jail, Namespace: &resourceMountIdentity{BootID: idLive, Namespace: 3}},
			{Kind: "materialised", Path: materialised, File: identity(materialised)},
			{Kind: "clone", Path: clone, File: identity(clone)},
		}}
	if err := r.validate(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(opts.procRoot, "self"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(opts.procRoot, "self", "mountinfo"), []byte("22 1 0:21 / /proc rw - proc proc rw\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return r, materialised, clone
}

func recoverDeadFixture(t *testing.T, opts restartInventoryOptions, r resourceJournalRecord) (*Manager, *ResourceJournal, RestartQuarantineReport) {
	t.Helper()
	j := openTestResourceJournal(t, t.TempDir())
	if err := j.beginRecord(r); err != nil {
		t.Fatal(err)
	}
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	if err := m.WithResourceJournal(j); err != nil {
		t.Fatal(err)
	}
	rep, err := m.recoverRestartQuarantine(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	return m, j, rep
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return !errors.Is(err, os.ErrNotExist)
}

func TestResourceRestartDeadRetiresGoneInstanceAndItsFiles(t *testing.T) {
	opts := restartFixture(t)
	r, materialised, clone := deadRecordFixture(t, opts)
	m, j, rep := recoverDeadFixture(t, opts, r)
	if rep.ReclaimedDeadRecords != 1 || rep.JournalRecords != 0 || rep.Slots != 0 {
		t.Fatalf("report = %+v; want the dead record retired and its slot free", rep)
	}
	if exists(materialised) || exists(clone) {
		t.Fatal("identity-verified temporary files of the dead instance were left on disk")
	}
	if _, err := j.dir.ReadFile(resourceRecordName(r.storageIdentity())); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("retired record still published")
	}
	if l, err := m.alloc.reserveNetwork("fresh"); err != nil || l.Slot != r.Lease.Slot {
		t.Fatalf("slot %d not reusable after retirement: %+v, %v", r.Lease.Slot, l, err)
	}
}

func TestResourceRestartDeadLeavesReusedPathAlone(t *testing.T) {
	opts := restartFixture(t)
	r, materialised, _ := deadRecordFixture(t, opts)
	if err := os.Remove(materialised); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(materialised, []byte("someone else's file"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, rep := recoverDeadFixture(t, opts, r)
	if rep.ReclaimedDeadRecords != 1 {
		t.Fatalf("report = %+v; the recorded file is already gone, so the record retires", rep)
	}
	if !exists(materialised) {
		t.Fatal("removed a file that only reused the recorded path")
	}
}

// Each case is a same-boot hazard ADR-478 names, or missing evidence: the
// record must stay quarantined and its files untouched.
func TestResourceRestartDeadKeepsAmbiguousRecords(t *testing.T) {
	writeProc := func(t *testing.T, opts restartInventoryOptions, pid int, uid int) string {
		t.Helper()
		dir := filepath.Join(opts.procRoot, fmt.Sprint(pid))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "status"), fmt.Appendf(nil, "Uid:\t%d\t%d\t%d\t%d\n", uid, uid, uid, uid), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte("sleep\x00"), 0o600); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, opts restartInventoryOptions, r *resourceJournalRecord)
	}{
		{"process_alive", func(t *testing.T, opts restartInventoryOptions, _ *resourceJournalRecord) {
			dir := writeProc(t, opts, deadTestPID, 0)
			fields := strings.Fields(strings.Repeat("0 ", 20))
			fields[0], fields[19] = "S", fmt.Sprint(deadTestTicks)
			stat := fmt.Sprintf("%d (firecracker) %s\n", deadTestPID, strings.Join(fields, " "))
			if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"slot_uid_held", func(t *testing.T, opts restartInventoryOptions, r *resourceJournalRecord) {
			writeProc(t, opts, 77, JailUIDBase+r.Lease.Slot)
		}},
		{"veth_name_present", func(t *testing.T, opts restartInventoryOptions, r *resourceJournalRecord) {
			if err := os.Mkdir(filepath.Join(opts.netRoot, r.Lease.VethHost), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{"renamed_link_present", func(t *testing.T, opts restartInventoryOptions, _ *resourceJournalRecord) {
			dir := filepath.Join(opts.netRoot, "eth9")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			_ = os.WriteFile(filepath.Join(dir, "ifindex"), []byte("12\n"), 0o600)
			_ = os.WriteFile(filepath.Join(dir, "address"), []byte("02:00:00:00:00:01\n"), 0o600)
		}},
		{"netns_held_by_process", func(t *testing.T, opts restartInventoryOptions, _ *resourceJournalRecord) {
			dir := writeProc(t, opts, 78, 0)
			if err := os.Mkdir(filepath.Join(dir, "ns"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(fmt.Sprintf("net:[%d]", deadTestNetns), filepath.Join(dir, "ns", "net")); err != nil {
				t.Fatal(err)
			}
		}},
		{"netns_held_by_mount", func(t *testing.T, opts restartInventoryOptions, _ *resourceJournalRecord) {
			line := fmt.Sprintf("40 22 0:4 net:[%d] /run/elsewhere rw - nsfs nsfs rw\n", deadTestNetns)
			if err := os.WriteFile(filepath.Join(opts.procRoot, "self", "mountinfo"), []byte(line), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"jail_present", func(t *testing.T, _ restartInventoryOptions, r *resourceJournalRecord) {
			for _, a := range r.Assets {
				if a.Kind == "jail" {
					if err := os.MkdirAll(a.Path, 0o700); err != nil {
						t.Fatal(err)
					}
				}
			}
		}},
		{"no_process_provenance", func(_ *testing.T, _ restartInventoryOptions, r *resourceJournalRecord) {
			r.Process = nil
		}},
		{"netns_not_checkpointed", func(_ *testing.T, _ restartInventoryOptions, r *resourceJournalRecord) {
			for i := range r.Assets {
				if r.Assets[i].Kind == "netns" {
					r.Assets[i].File, r.Assets[i].Mount = nil, nil
				}
			}
		}},
		{"mountinfo_unreadable", func(t *testing.T, opts restartInventoryOptions, _ *resourceJournalRecord) {
			if err := os.Remove(filepath.Join(opts.procRoot, "self", "mountinfo")); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := restartFixture(t)
			r, materialised, clone := deadRecordFixture(t, opts)
			tc.setup(t, opts, &r)
			_, _, rep := recoverDeadFixture(t, opts, r)
			if rep.ReclaimedDeadRecords != 0 || rep.JournalRecords != 1 {
				t.Fatalf("report = %+v; want the record kept in quarantine", rep)
			}
			if !exists(materialised) || !exists(clone) {
				t.Fatal("removed files of a record that stays quarantined")
			}
		})
	}
}
