package fcvm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// recordingRunner captures teardown argv so tests can assert on the
// exact commands issued rather than on side effects a unit test cannot
// observe (no real netns on a CI runner).
type recordingRunner struct{ cmds [][]string }

func (r *recordingRunner) Run(_ context.Context, argv []string) error {
	r.cmds = append(r.cmds, argv)
	return nil
}

func (r *recordingRunner) ran(want ...string) bool {
	for _, c := range r.cmds {
		if strings.Join(c, " ") == strings.Join(want, " ") {
			return true
		}
	}
	return false
}

// reapFixture builds a jail root with the named instance chroots, aged
// past the default MinAge so they are eligible.
func reapFixture(t *testing.T, ids ...string) string {
	t.Helper()
	root := t.TempDir()
	old := time.Now().Add(-1 * time.Hour)
	for _, id := range ids {
		dir := filepath.Join(root, id)
		if err := os.MkdirAll(filepath.Join(dir, "root"), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", id, err)
		}
		if err := os.Chtimes(dir, old, old); err != nil {
			t.Fatalf("chtimes %s: %v", id, err)
		}
	}
	return root
}

const (
	idDead  = "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa"
	idLive  = "bbbbbbbb-2222-4222-8222-bbbbbbbbbbbb"
	idOther = "cccccccc-3333-4333-8333-cccccccccccc"
)

func alwaysDead(context.Context, string) (bool, error) { return false, nil }

// TestReapOrphanedJails_SparesInstancesTheSchedulerCallsLive is the
// test that matters most, and it encodes a real near-miss.
//
// On 2026-09-04 a production compute node held 25 running Firecracker
// processes after a vmmd restart. 23 were orphans the scheduler had
// already written off, but 2 were not: one RUNNING and serving traffic,
// one mid-SNAPSHOTTING. The obvious implementation — "vmmd just
// started, so anything on disk is stale" — would have killed a
// customer's live VM and destroyed a snapshot in flight.
//
// The gate is therefore the scheduler's durable view, never vmmd's
// memory. If this test is ever "simplified" by dropping IsLive, that
// outage is back.
func TestReapOrphanedJails_SparesInstancesTheSchedulerCallsLive(t *testing.T) {
	root := reapFixture(t, idDead, idLive)
	runner := &recordingRunner{}

	rep, err := ReapOrphanedJails(context.Background(), ReapOptions{
		JailRoot: root,
		Runner:   runner,
		IsLive: func(_ context.Context, id string) (bool, error) {
			return id == idLive, nil
		},
	})
	if err != nil {
		t.Fatalf("ReapOrphanedJails: %v", err)
	}
	if rep.Reaped != 1 || rep.SkippedLive != 1 {
		t.Errorf("report = %+v, want Reaped=1 SkippedLive=1", rep)
	}
	if _, err := os.Stat(filepath.Join(root, idLive)); err != nil {
		t.Errorf("live instance chroot was removed (%v); a serving VM would have been destroyed", err)
	}
	if _, err := os.Stat(filepath.Join(root, idDead)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("dead instance chroot still present; the leak is not fixed")
	}
	if runner.ran("ip", "netns", "del", "fc-"+idLive) {
		t.Error("tore down the netns of an instance the scheduler considers live")
	}
	if !runner.ran("ip", "netns", "del", "fc-"+idDead) {
		t.Errorf("did not delete the orphan's netns; cmds=%v", runner.cmds)
	}
}

// TestReapOrphanedJails_LivenessErrorSkips pins the fail-safe
// direction. IsLive returns an error when the store is unreachable; if
// that collapsed to "not live", a Postgres outage would escalate into
// vmmd killing every VM on the node. Unknown must mean "leave it".
func TestReapOrphanedJails_LivenessErrorSkips(t *testing.T) {
	root := reapFixture(t, idDead)
	rep, err := ReapOrphanedJails(context.Background(), ReapOptions{
		JailRoot: root,
		Runner:   &recordingRunner{},
		IsLive: func(context.Context, string) (bool, error) {
			return false, errors.New("dial tcp: connection refused")
		},
	})
	if err != nil {
		t.Fatalf("ReapOrphanedJails: %v", err)
	}
	if rep.Reaped != 0 || rep.SkippedUnknown != 1 {
		t.Errorf("report = %+v, want Reaped=0 SkippedUnknown=1", rep)
	}
	if _, err := os.Stat(filepath.Join(root, idDead)); err != nil {
		t.Error("reaped an instance whose liveness could not be determined; a store outage must not kill VMs")
	}
}

// TestReapOrphanedJails_NilIsLiveRefuses pins that the sweep cannot be
// constructed without its gate. A nil IsLive is a wiring bug, and the
// dangerous reading of it ("no gate, so reap everything") must be
// impossible to reach by accident.
func TestReapOrphanedJails_NilIsLiveRefuses(t *testing.T) {
	root := reapFixture(t, idDead)
	if _, err := ReapOrphanedJails(context.Background(), ReapOptions{JailRoot: root, Runner: &recordingRunner{}}); err == nil {
		t.Fatal("nil IsLive was accepted; want an error rather than an ungated sweep")
	}
	if _, err := os.Stat(filepath.Join(root, idDead)); err != nil {
		t.Error("chroot removed despite the refusal")
	}
}

// TestReapOrphanedJails_SkipsYoungChroots pins the race guard. A chroot
// created moments ago may belong to a wake in flight whose instance row
// schedd has not committed yet; IsLive would say "not live" and the
// sweep would kill a VM that is mid-boot.
func TestReapOrphanedJails_SkipsYoungChroots(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, idDead), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rep, err := ReapOrphanedJails(context.Background(), ReapOptions{
		JailRoot: root,
		Runner:   &recordingRunner{},
		IsLive:   alwaysDead,
	})
	if err != nil {
		t.Fatalf("ReapOrphanedJails: %v", err)
	}
	if rep.Reaped != 0 || rep.SkippedYoung != 1 {
		t.Errorf("report = %+v, want Reaped=0 SkippedYoung=1 (a just-created chroot may be a wake in flight)", rep)
	}
}

// TestReapOrphanedJails_IgnoresNonInstanceEntries keeps the sweep from
// touching anything that is not plausibly an instance chroot. The jail
// root is a real directory an operator may look inside; a stray file or
// scratch directory must never be a kill target.
func TestReapOrphanedJails_IgnoresNonInstanceEntries(t *testing.T) {
	root := reapFixture(t, idDead)
	old := time.Now().Add(-1 * time.Hour)
	for _, name := range []string{"scratch", "not-a-uuid", "0123456789abcdef"} {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		_ = os.Chtimes(p, old, old)
	}
	if err := os.WriteFile(filepath.Join(root, "README"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	rep, err := ReapOrphanedJails(context.Background(), ReapOptions{
		JailRoot: root, Runner: &recordingRunner{}, IsLive: alwaysDead,
	})
	if err != nil {
		t.Fatalf("ReapOrphanedJails: %v", err)
	}
	if rep.Scanned != 1 || rep.Reaped != 1 {
		t.Errorf("report = %+v, want Scanned=1 Reaped=1 (only the UUID directory is a candidate)", rep)
	}
	for _, name := range []string{"scratch", "not-a-uuid", "0123456789abcdef", "README"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Errorf("removed non-instance entry %q: %v", name, err)
		}
	}
}

// TestReapOrphanedJails_MissingJailRootIsNotAnError — a node that has
// never booted a VM has no jail tree, and vmmd must still start.
func TestReapOrphanedJails_MissingJailRootIsNotAnError(t *testing.T) {
	rep, err := ReapOrphanedJails(context.Background(), ReapOptions{
		JailRoot: filepath.Join(t.TempDir(), "absent"),
		Runner:   &recordingRunner{},
		IsLive:   alwaysDead,
	})
	if err != nil {
		t.Fatalf("missing jail root returned %v, want nil", err)
	}
	if rep.Scanned != 0 {
		t.Errorf("Scanned = %d, want 0", rep.Scanned)
	}
}

// --- process discovery -------------------------------------------------

// fakeProc writes a /proc-shaped tree so the cmdline scan can be tested
// without spawning processes.
func fakeProc(t *testing.T, pids map[int][]string) string {
	t.Helper()
	root := t.TempDir()
	for pid, argv := range pids {
		d := filepath.Join(root, fmt.Sprint(pid))
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(d, "cmdline"), []byte(strings.Join(argv, "\x00")), 0o644); err != nil {
			t.Fatalf("write cmdline: %v", err)
		}
	}
	// A non-numeric entry, as /proc really has.
	if err := os.MkdirAll(filepath.Join(root, "self"), 0o755); err != nil {
		t.Fatalf("mkdir self: %v", err)
	}
	return root
}

// TestFindFirecrackerPID_MatchesArgumentPairNotSubstring pins that
// process selection matches the `--id <uuid>` PAIR. A substring search
// over the raw cmdline would also match a UUID that merely appears in
// some other argument — a storage key, a chroot path — and this code
// kills what it matches.
func TestFindFirecrackerPID_MatchesArgumentPairNotSubstring(t *testing.T) {
	proc := fakeProc(t, map[int][]string{
		// Mentions idDead only inside an unrelated path argument.
		41: {"/usr/bin/imaged", "--cache", "/var/lib/faas/cache/" + idDead + ".ext4"},
		// The real one.
		42: {"/firecracker", "--id", idDead, "--uid", "20007", "--api-sock", "api.sock"},
	})
	pid, slot, ok := findFirecrackerPID(proc, idDead)
	if !ok {
		t.Fatal("no process found for the instance")
	}
	if pid != 42 {
		t.Errorf("pid = %d, want 42 (a substring match would have selected 41 and killed the wrong process)", pid)
	}
	if slot != 7 {
		t.Errorf("slot = %d, want 7 (recovered from --uid %d+7)", slot, JailUIDBase)
	}
}

// TestFindFirecrackerPID_AbsentProcess — a chroot whose VM already
// exited is normal; the sweep must still clean the directory up.
func TestFindFirecrackerPID_AbsentProcess(t *testing.T) {
	proc := fakeProc(t, map[int][]string{7: {"/firecracker", "--id", idOther}})
	if _, _, ok := findFirecrackerPID(proc, idDead); ok {
		t.Error("found a process for an instance that is not running")
	}
}

// TestSlotFromUIDArg_RejectsOutOfRange keeps a malformed or hostile
// --uid from naming a veth that belongs to a different, live instance.
// Returning -1 suppresses the link delete entirely, which is the safe
// direction.
func TestSlotFromUIDArg_RejectsOutOfRange(t *testing.T) {
	cases := map[string][]string{
		"below base": {"--uid", "10"},
		"above max":  {"--uid", fmt.Sprint(JailUIDBase + MaxSlots + 1)},
		"not a int":  {"--uid", "root"},
		"absent":     {"--api-sock", "api.sock"},
	}
	for name, argv := range cases {
		if got := slotFromUIDArg(argv); got != -1 {
			t.Errorf("%s: slot = %d, want -1 (must not guess a veth name)", name, got)
		}
	}
	if got := slotFromUIDArg([]string{"--uid", fmt.Sprint(JailUIDBase)}); got != 0 {
		t.Errorf("slot 0 case = %d, want 0", got)
	}
}

// TestLooksLikeInstanceID pins the shape check that decides whether a
// directory is a candidate at all.
func TestLooksLikeInstanceID(t *testing.T) {
	if !looksLikeInstanceID(idDead) {
		t.Errorf("%q rejected; real chroots are named by instance UUID", idDead)
	}
	for _, bad := range []string{
		"", "scratch", "0123456789abcdef",
		strings.Repeat("a", 36),                // right length, no dashes
		"aaaaaaaa-1111-4111-8111-aaaaaaaaaaa",  // 35
		"aaaaaaaa-1111-4111-8111-aaaaaaaaaaaz", // non-hex
	} {
		if looksLikeInstanceID(bad) {
			t.Errorf("%q accepted as an instance id", bad)
		}
	}
}
