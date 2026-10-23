// adr: 473 — each wake's resource-journal cost is reported per restore
// (vmmd_restore_phase_duration_seconds journal_sync / journal_lock_wait).

package fcvm

import (
	"testing"
	"time"
)

// slowDirectorySync makes every directory fsync take at least d, so the
// sync time a test reads back has a known floor.
func slowDirectorySync(t *testing.T, j *ResourceJournal, d time.Duration) {
	t.Helper()
	real := j.directorySync
	j.directorySync = func() error {
		time.Sleep(d)
		return real()
	}
}

func TestResourceJournalTimingIsPerWake(t *testing.T) {
	j := openTestResourceJournal(t, t.TempDir())
	slowDirectorySync(t, j, 2*time.Millisecond)
	a, b := journalTestLease("wake-a", 1), journalTestLease("wake-b", 2)
	for _, step := range []func() error{
		func() error { return j.begin(a) },
		func() error { return j.begin(b) },
		func() error {
			return j.recordProcess(a, resourceProcessIdentity{BootID: "0f8e2a52-6c1d-4b8e-9a3f-2d7c5b1e4a90", PID: 42, StartTicks: 7})
		},
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		instance    string
		wantCommits int
	}{
		{instance: "wake-a", wantCommits: 2},
		{instance: "wake-b", wantCommits: 1},
		{instance: "never-written", wantCommits: 0},
	}
	for _, tc := range cases {
		got := j.takeTiming(tc.instance)
		if got.Commits != tc.wantCommits {
			t.Errorf("%s: commits = %d, want %d", tc.instance, got.Commits, tc.wantCommits)
		}
		if floor := time.Duration(tc.wantCommits) * 2 * time.Millisecond; got.Sync < floor {
			t.Errorf("%s: sync = %v, want at least the %v of directory fsyncs", tc.instance, got.Sync, floor)
		}
		if again := j.takeTiming(tc.instance); again != (resourceJournalTiming{}) {
			t.Errorf("%s: a second take returned %+v, want it cleared", tc.instance, again)
		}
	}
}

// Commits serialize on the journal lock, so a wake's staging also waits for
// other wakes' fsyncs. That wait is reported apart from its own fsyncs.
func TestResourceJournalTimingCountsLockWait(t *testing.T) {
	j := openTestResourceJournal(t, t.TempDir())
	j.mu.Lock()
	go func() {
		time.Sleep(30 * time.Millisecond)
		j.mu.Unlock()
	}()
	if err := j.begin(journalTestLease("waits", 1)); err != nil {
		t.Fatal(err)
	}
	got := j.takeTiming("waits")
	if got.LockWait < 20*time.Millisecond {
		t.Fatalf("lock wait = %v, want the ~30 ms the lock was held elsewhere", got.LockWait)
	}
	if err := j.begin(journalTestLease("no-wait", 2)); err != nil {
		t.Fatal(err)
	}
	if got := j.takeTiming("no-wait"); got.LockWait != 0 || got.Commits != 1 {
		t.Fatalf("uncontended commit = %+v, want one commit and no lock wait", got)
	}
}

// Timing for an instance whose record is gone must not outlive it.
func TestResourceJournalTimingRetiredWithRecord(t *testing.T) {
	cases := []struct {
		name     string
		noRecord bool
	}{
		{name: "record retired"},
		// e.g. the pending file could not be created: noteCommit ran, but
		// no record was published for forget to retire.
		{name: "commit failed before its record existed", noRecord: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := openTestResourceJournal(t, t.TempDir())
			l := journalTestLease("parked", 1)
			if tc.noRecord {
				j.mu.Lock()
				j.noteCommit(l.Instance)
				j.mu.Unlock()
			} else if err := j.begin(l); err != nil {
				t.Fatal(err)
			}
			if err := j.forget(l); err != nil {
				t.Fatal(err)
			}
			j.mu.Lock()
			left := len(j.timing)
			j.mu.Unlock()
			if left != 0 {
				t.Fatalf("%d timing entries left after the instance was retired", left)
			}
		})
	}
}

func TestResourceJournalTimingKey(t *testing.T) {
	cases := []struct {
		name string
		r    resourceJournalRecord
		want string
	}{
		{name: "lease", r: resourceJournalRecord{Lease: Lease{Instance: "i1"}}, want: "i1"},
		{name: "unclaimed spare", r: resourceJournalRecord{Lease: Lease{Instance: "spare"}, Prepared: &resourcePreparedNetwork{Source: "spare"}}, want: "spare"},
		{name: "spare handed to a wake", r: resourceJournalRecord{Lease: Lease{Instance: "spare"}, Prepared: &resourcePreparedNetwork{Source: "spare", Target: "i2"}}, want: "i2"},
		{name: "adopted spare", r: resourceJournalRecord{Lease: Lease{Instance: "i2"}, Prepared: &resourcePreparedNetwork{Source: "spare", Target: "i2"}}, want: "i2"},
	}
	for _, tc := range cases {
		if got := tc.r.timingKey(); got != tc.want {
			t.Errorf("%s: key = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestResourceJournalTakeTimingNilJournal(t *testing.T) {
	var j *ResourceJournal
	if got := j.takeTiming("i1"); got != (resourceJournalTiming{}) {
		t.Fatalf("nil journal timing = %+v, want zero", got)
	}
}
