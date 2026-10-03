// adr: 473
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func openTestResourceJournal(t *testing.T, path string) *ResourceJournal {
	t.Helper()
	// testing.TempDir's numbered leaf can be 0755; journal storage is 0700.
	if err := os.Chmod(path, 0o700); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	j, err := OpenResourceJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	return j
}

func journalTestLease(id string, slot int) Lease {
	l := leaseForSlot(id, slot)
	l.Plan, l.MemoryMaxMiB = api.PlanPro, 512
	return l
}

func TestResourceJournalExclusiveDurableAndConcurrent(t *testing.T) {
	path := t.TempDir()
	j := openTestResourceJournal(t, path)
	if second, err := OpenResourceJournal(path); err == nil {
		_ = second.Close()
		t.Fatal("second journal writer admitted")
	}
	var wg sync.WaitGroup
	for slot := 0; slot < 20; slot++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			if err := j.begin(journalTestLease(fmt.Sprintf("instance-%d", slot), slot)); err != nil {
				t.Error(err)
			}
		}(slot)
	}
	wg.Wait()
	if err := j.begin(journalTestLease("duplicate-slot", 0)); err == nil {
		t.Fatal("duplicate slot admitted")
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openTestResourceJournal(t, path)
	records, err := reopened.snapshot()
	if err != nil || len(records) != 20 {
		t.Fatalf("recovered records: %d, %v", len(records), err)
	}
	for _, r := range records {
		if err := reopened.forget(r.Lease); err != nil {
			t.Fatal(err)
		}
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	final := openTestResourceJournal(t, path)
	records, err = final.snapshot()
	if err != nil || len(records) != 0 {
		t.Fatalf("retired records recovered: %d, %v", len(records), err)
	}
}

type journalOrderingRunner struct {
	*fakeRunner
	journal *ResourceJournal
	t       *testing.T
}

func (r *journalOrderingRunner) Run(ctx context.Context, argv []string) error {
	if strings.Contains(strings.Join(argv, " "), " add ") {
		records, err := r.journal.snapshot()
		if err != nil || len(records) != 1 {
			r.t.Fatalf("network creation before journal intent: %v", err)
		}
	}
	return r.fakeRunner.Run(ctx, argv)
}

func TestResourceJournalBootOrderingAndConfirmedRetirement(t *testing.T) {
	for _, job := range []bool{false, true} {
		t.Run(fmt.Sprint("job=", job), func(t *testing.T) {
			j := openTestResourceJournal(t, t.TempDir())
			vmm := &fakeVMM{}
			run := &journalOrderingRunner{&fakeRunner{}, j, t}
			m := newTestManager(run, vmm)
			if err := m.WithResourceJournal(j); err != nil {
				t.Fatal(err)
			}
			installNamespaceFixture(m)
			var err error
			if job {
				_, err = m.BootJob(t.Context(), JobBootRequest{Instance: idLive, Plan: api.PlanPro, MemSizeMiB: 512, KernelKey: "kernel", BaseKey: "base", ImageRef: "job.ext4", AccountID: "account", RunID: "run", TaskTimeoutSec: 30, LeaseToken: "fixture-token", Env: map[string]string{"PRIVATE_FIXTURE": "fixture-env-value"}, Command: []string{"/bin/true"}, VcpuCount: 1})
			} else {
				_, err = m.Wake(t.Context(), wakeReq(idLive, nil))
			}
			if err != nil {
				t.Fatal(err)
			}
			m.live[idLive].Lease.CPUMillicores = 1000 // Live CPU policy must not prevent retirement.
			body, err := j.dir.ReadFile(resourceRecordName(idLive))
			if err != nil || strings.Contains(string(body), "fixture-token") || strings.Contains(string(body), "fixture-env-value") || strings.Contains(string(body), "/bin/true") {
				t.Fatal("journal stored execution credentials or command")
			}
			injected := errors.New("directory fsync unavailable")
			syncDir := j.directorySync
			j.directorySync = func() error { return injected }
			if err := m.Destroy(t.Context(), idLive); !errors.Is(err, injected) {
				t.Fatalf("uncertain journal retirement: %v", err)
			}
			if m.LeasedCount() != 1 || !m.HasInstanceOwnership(idLive) {
				t.Fatal("uncertain retirement released ownership")
			}
			if _, err := m.Wake(t.Context(), wakeReq(idLive, nil)); err == nil {
				t.Fatal("boot admitted while journal retirement pending")
			}
			j.directorySync = syncDir
			if err := m.Destroy(t.Context(), idLive); err != nil {
				t.Fatal(err)
			}
			records, err := j.snapshot()
			if err != nil || len(records) != 0 || m.LeasedCount() != 0 {
				t.Fatal("confirmed journal retirement retained a lease")
			}
		})
	}
}

func TestResourceJournalWriteFailureRejectsBoot(t *testing.T) {
	j := openTestResourceJournal(t, t.TempDir())
	injected := errors.New("intent fsync unavailable")
	syncDir := j.directorySync
	j.directorySync = func() error { return injected }
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)
	if err := m.WithResourceJournal(j); err != nil {
		t.Fatal(err)
	}
	installNamespaceFixture(m)
	if _, err := m.Wake(t.Context(), wakeReq(idLive, nil)); !errors.Is(err, injected) {
		t.Fatalf("boot result: %v", err)
	}
	if vmm.bootCount != 0 {
		t.Fatal("guest launched after failed intent commit")
	}
	for _, argv := range run.commands {
		if strings.Contains(strings.Join(argv, " "), " add ") {
			t.Fatal("network created after failed intent commit")
		}
	}
	if m.LeasedCount() != 1 {
		t.Fatal("uncertain intent/retirement released slot")
	}
	j.directorySync = syncDir
	if err := m.Destroy(t.Context(), idLive); err != nil {
		t.Fatal(err)
	}
	if m.LeasedCount() != 0 {
		t.Fatal("cleanup retry did not release slot")
	}
}

func journalProcessFixture(t *testing.T, opts restartInventoryOptions, pid int, ticks uint64) {
	t.Helper()
	dir := filepath.Join(opts.procRoot, "sys/kernel/random")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "boot_id"), []byte(idOther+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(strings.Repeat("0 ", 20))
	fields[0], fields[19] = "S", fmt.Sprint(ticks)
	stat := fmt.Sprintf("%d (fire cracker) test) %s\n", pid, strings.Join(fields, " "))
	if err := os.WriteFile(filepath.Join(opts.procRoot, fmt.Sprint(pid), "stat"), []byte(stat), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResourceJournalRecoveredIntentQuarantinesWithoutResources(t *testing.T) {
	path := t.TempDir()
	j := openTestResourceJournal(t, path)
	lease := journalTestLease("build-"+idLive, 0)
	if err := j.begin(lease); err != nil {
		t.Fatal(err)
	}
	_ = j.Close()
	j = openTestResourceJournal(t, path)
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	if err := m.WithResourceJournal(j); err != nil {
		t.Fatal(err)
	}
	rep, err := m.recoverRestartQuarantine(t.Context(), restartFixture(t))
	if err != nil || rep.JournalRecords != 1 || rep.Slots != 1 || rep.Processes != 0 {
		t.Fatalf("intent inventory: %+v, %v", rep, err)
	}
	fresh, err := m.alloc.Acquire("fresh")
	if err != nil || fresh.Slot != 1 {
		t.Fatalf("fresh allocator reused intent: %+v, %v", fresh, err)
	}
	if m.HasInstanceOwnership(lease.Instance) {
		t.Fatal("journal intent became lifecycle ownership")
	}
	if err := m.Destroy(t.Context(), lease.Instance); !errors.Is(err, ErrRestartQuarantine) {
		t.Fatalf("unowned stop acknowledged: %v", err)
	}
}

func TestResourceJournalProcessIncarnationMatches(t *testing.T) {
	for _, kind := range []string{"match", "reused_pid", "other_boot", "other_uid", "other_instance", "gone", "missing_boot"} {
		t.Run(kind, func(t *testing.T) {
			opts := restartFixture(t)
			restartProcessFixture(t, opts, 101, 0, "firecracker", "--id", idLive)
			journalProcessFixture(t, opts, 101, 77)
			identity, err := readResourceProcessIdentity(opts.procRoot, 101)
			if err != nil || identity.StartTicks != 77 {
				t.Fatalf("identity parser: %+v, %v", identity, err)
			}
			path := t.TempDir()
			j := openTestResourceJournal(t, path)
			lease := journalTestLease(idLive, 0)
			if err := j.begin(lease); err != nil {
				t.Fatal(err)
			}
			if err := j.recordProcess(lease, identity); err != nil {
				t.Fatal(err)
			}
			_ = j.Close()
			j = openTestResourceJournal(t, path)
			switch kind {
			case "reused_pid":
				journalProcessFixture(t, opts, 101, 78)
			case "other_boot":
				_ = os.WriteFile(filepath.Join(opts.procRoot, "sys/kernel/random/boot_id"), []byte(idDead), 0o600)
			case "other_uid":
				_ = os.WriteFile(filepath.Join(opts.procRoot, "101/status"), []byte("Uid: 20001 20001 20001 20001\n"), 0o600)
			case "other_instance":
				_ = os.WriteFile(filepath.Join(opts.procRoot, "101/cmdline"), []byte("firecracker\x00--id\x00"+idOther+"\x00"), 0o600)
			case "gone":
				_ = os.RemoveAll(filepath.Join(opts.procRoot, "101"))
			case "missing_boot":
				_ = os.Remove(filepath.Join(opts.procRoot, "sys/kernel/random/boot_id"))
			}
			m := newTestManager(&fakeRunner{}, &fakeVMM{})
			if err := m.WithResourceJournal(j); err != nil {
				t.Fatal(err)
			}
			rep, err := m.recoverRestartQuarantine(t.Context(), opts)
			if kind == "missing_boot" {
				if err == nil || !m.alloc.pristine() {
					t.Fatal("unreadable process provenance mutated inventory")
				}
				return
			}
			want := 0
			if kind == "match" {
				want = 1
			}
			if err != nil || rep.JournalRecords != 1 || rep.JournalProcessMatches != want {
				t.Fatalf("provenance: %+v, %v", rep, err)
			}
			if m.HasInstanceOwnership(idLive) {
				t.Fatal("process provenance authorized recovered failure reports")
			}
			fresh, err := m.alloc.Acquire("fresh")
			if err != nil || fresh.Slot == lease.Slot {
				t.Fatal("mismatched process freed journal slot")
			}
		})
	}
}

func TestResourceJournalRejectsCorruptStorage(t *testing.T) {
	for _, kind := range []string{"version", "uid", "duplicate_slot", "trailing", "unknown_field", "permissions", "symlink", "hardlink", "unexpected"} {
		t.Run(kind, func(t *testing.T) {
			path := t.TempDir()
			j := openTestResourceJournal(t, path)
			lease := journalTestLease(idLive, 0)
			if err := j.begin(lease); err != nil {
				t.Fatal(err)
			}
			_ = j.Close()
			name := filepath.Join(path, resourceRecordName(idLive))
			b, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			r := resourceJournalRecord{Version: 1, Lease: lease}
			switch kind {
			case "version":
				r.Version = 99
				b, _ = json.Marshal(r)
			case "uid":
				r.Lease.UID++
				b, _ = json.Marshal(r)
			case "duplicate_slot":
				r.Lease = journalTestLease(idOther, 0)
				other, _ := json.Marshal(r)
				_ = os.WriteFile(filepath.Join(path, resourceRecordName(idOther)), other, 0o600)
			case "trailing":
				b = append(b, []byte(" {}")...)
			case "unknown_field":
				b = append(b[:len(b)-1], []byte(",\"unknown\":true}")...)
			case "permissions":
				_ = os.Chmod(name, 0o640)
			case "symlink":
				_ = os.Remove(name)
				_ = os.Symlink(filepath.Join(t.TempDir(), "outside"), name)
			case "hardlink":
				_ = os.Link(name, filepath.Join(path, "extra"))
			case "unexpected":
				_ = os.Mkdir(filepath.Join(path, "foreign"), 0o700)
			}
			if kind == "version" || kind == "uid" || kind == "trailing" || kind == "unknown_field" {
				if err := os.WriteFile(name, b, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if bad, err := OpenResourceJournal(path); err == nil {
				_ = bad.Close()
				t.Fatal("corrupt journal admitted")
			}
		})
	}
}
