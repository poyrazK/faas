//go:build linux || darwin

// adr: 532 — helper producer fences precede physical resource acknowledgement.
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// This backend exercises durable ordering with real gated subprocess fixtures.
// Its acknowledgements model cgroups; they are not native kernel acceptance.
type nativeHelperGroupsFixture struct {
	mu        sync.Mutex
	groups    map[string]nativeHostHelperGroup
	commands  map[string]*exec.Cmd
	retireErr error
	creates   int
	retired   int
}

func newNativeHelperGroupsFixture() *nativeHelperGroupsFixture {
	return &nativeHelperGroupsFixture{groups: make(map[string]nativeHostHelperGroup), commands: make(map[string]*exec.Cmd)}
}

func (g *nativeHelperGroupsFixture) Plan(id string) (nativeHostHelperGroup, error) {
	return nativeHostHelperGroup{Path: filepath.Join("fixture-service", nativeHostHelperScope, id)}, nil
}

func (g *nativeHelperGroupsFixture) Create(group nativeHostHelperGroup) (nativeHostHelperGroup, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.creates++
	group.Device, group.Inode = 17, uint64(g.creates)
	g.groups[group.Path] = group
	return group, nil
}

func (g *nativeHelperGroupsFixture) Attach(cmd *exec.Cmd, group nativeHostHelperGroup) (io.Closer, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.groups[group.Path] != group {
		return nil, errors.New("fixture: changed group")
	}
	g.commands[group.Path] = cmd
	return io.NopCloser(strings.NewReader("")), nil
}

func (g *nativeHelperGroupsFixture) Retire(ctx context.Context, group nativeHostHelperGroup) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if g.retireErr != nil {
		return g.retireErr
	}
	if current, ok := g.groups[group.Path]; ok && group.Inode != 0 && current != group {
		return errors.New("fixture: changed group identity")
	}
	if cmd := g.commands[group.Path]; cmd != nil && cmd.Process != nil {
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
	}
	g.retired++
	delete(g.groups, group.Path)
	return nil
}

func (g *nativeHelperGroupsFixture) Removed(group nativeHostHelperGroup) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, found := g.groups[group.Path]; found {
		return errors.New("fixture: group reappeared")
	}
	return nil
}

func (g *nativeHelperGroupsFixture) Inventory(_ []nativeHostHelperRecord) error { return nil }

func nativeHelperJournalFixture(t *testing.T) (*nativeHostHelperJournal, nativeLaunchRecord, *nativeHelperGroupsFixture) {
	t.Helper()
	ownerJournal, lease := preparedNativeJournal(t)
	owner, err := ownerJournal.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	groups := newNativeHelperGroupsFixture()
	journal := &nativeHostHelperJournal{owner: ownerJournal, groups: groups, startTime: func(int) (uint64, error) { return 1234, nil }}
	return journal, owner, groups
}

func nativeHelperFixtureCommand(t *testing.T) (*exec.Cmd, string) {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "execution")
	t.Setenv("GREGALE_NATIVE_LAUNCH_FIXTURE", "1")
	t.Setenv("GREGALE_NATIVE_LAUNCH_MARKER", marker)
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	cmd, err := newNativeHostHelperCommand(os.Args[0], []string{os.Args[0]})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	return cmd, marker
}

func TestNativeHostHelperPublishesIncarnationBeforeExecutionAndJoins(t *testing.T) {
	j, owner, groups := nativeHelperJournalFixture(t)
	cmd, marker := nativeHelperFixtureCommand(t)
	authorized := false
	j.writeValue = func(path string, record nativeHostHelperRecord) error {
		if record.Launch.Authorized && !record.Launch.Revoked {
			authorized = true
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("helper executed before incarnation publication")
			}
			if record.Launch.PID != cmd.Process.Pid || record.Group.Inode == 0 {
				t.Fatal("authorization lacked process or cgroup identity")
			}
		}
		return writeNativeJournalValue(path, record)
	}
	if err := j.run(t.Context(), owner, cmd, time.Second); err != nil {
		t.Fatal(err)
	}
	if !authorized || cmd.ProcessState == nil || groups.retired != 1 {
		t.Fatal("helper escaped publication, single Wait ownership or cgroup retirement")
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "authorized" {
		t.Fatalf("execution marker=%s err=%v", data, err)
	}
	frames, err := j.records(owner)
	if err != nil || len(frames) != 1 || !frames[0].Launch.ResourcesRemoved {
		t.Fatalf("completed command frames=%+v err=%v", frames, err)
	}
	if _, err := j.owner.records(t.Context()); err != nil {
		t.Fatal("host helper journal prevented current launch discovery:", err)
	}
}

func TestNativeHostHelperFailedPublicationNeverExecutesAndStillJoins(t *testing.T) {
	for _, stage := range []string{"inode", "incarnation", "start_time"} {
		t.Run(stage, func(t *testing.T) {
			j, owner, groups := nativeHelperJournalFixture(t)
			cmd, marker := nativeHelperFixtureCommand(t)
			cause := errors.New("publication failed")
			j.writeValue = func(path string, record nativeHostHelperRecord) error {
				if !record.Launch.Revoked && (stage == "inode" && record.Group.Inode != 0 || stage == "incarnation" && record.Launch.Authorized) {
					return cause
				}
				return writeNativeJournalValue(path, record)
			}
			if stage == "start_time" {
				j.startTime = func(int) (uint64, error) { return 0, cause }
			}
			if err := j.run(t.Context(), owner, cmd, time.Second); !errors.Is(err, cause) {
				t.Fatalf("run=%v", err)
			}
			if stage != "inode" && cmd.ProcessState == nil {
				t.Fatal("started helper lost its single Wait owner")
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unpublished child crossed its execution gate")
			}
			frames, err := j.records(owner)
			if err != nil || len(frames) != 1 || !frames[0].Launch.ResourcesRemoved || frames[0].Launch.Authorized || groups.retired != 1 {
				t.Fatalf("failed launch retirement=%+v err=%v", frames, err)
			}
		})
	}
}

func TestNativeHostHelperCancellationJoinsRunningChild(t *testing.T) {
	j, owner, _ := nativeHelperJournalFixture(t)
	t.Setenv("GREGALE_NATIVE_HOLD_HELPER_FIXTURE", "1")
	cmd, marker := nativeHelperFixtureCommand(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- j.run(ctx, owner, cmd, time.Second) }()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case <-deadline.C:
			t.Fatal("helper never reached execution")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || cmd.ProcessState == nil {
			t.Fatalf("cancellation returned before process join: err=%v", err)
		}
	case <-deadline.C:
		t.Fatal("cancellation stranded a helper")
	}
	if err := j.requireRetired(owner); err != nil {
		t.Fatal(err)
	}
}

func preparedNativeHelperFrame(t *testing.T, j *nativeHostHelperJournal, owner nativeLaunchRecord) nativeHostHelperRecord {
	t.Helper()
	cmd, _ := nativeHelperFixtureCommand(t)
	record, started, err := j.launch(t.Context(), owner, cmd)
	if err != nil || !started {
		t.Fatalf("launch started=%v err=%v", started, err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	return record // Leader exited; durable retirement remains unfinished.
}

func TestNativeHostHelperUncertainRetirementFencesAcknowledgementAndReplacement(t *testing.T) {
	j, owner, groups := nativeHelperJournalFixture(t)
	frame := preparedNativeHelperFrame(t, j, owner)
	retiredOwner, err := j.owner.revoke(t.Context(), owner.Lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.owner.confirmExit(t.Context(), retiredOwner); err != nil {
		t.Fatal(err)
	}
	retiredOwner, err = j.owner.read(owner.Lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("kernel exit acknowledgement uncertain")
	groups.retireErr = cause
	if err := j.retire(t.Context(), owner, frame.Launch.Generation); !errors.Is(err, cause) {
		t.Fatalf("retire=%v", err)
	}
	if err := j.owner.confirmResourcesRemoved(t.Context(), retiredOwner); err == nil {
		t.Fatal("uncertain helper released VM resources")
	}
	if _, err := j.owner.replace(t.Context(), owner.Lease, owner.Generation, false); err == nil {
		t.Fatal("restore fallback outran uncertain helper retirement")
	}
	cmd, _ := nativeHelperFixtureCommand(t)
	if _, started, err := j.launch(t.Context(), owner, cmd); err == nil || started || groups.creates != 1 {
		t.Fatal("revoked owner authorized another host helper")
	}
	frames, err := j.records(owner)
	if err != nil || !frames[0].Launch.Revoked || frames[0].Launch.ExitConfirmed || frames[0].Launch.ResourcesRemoved {
		t.Fatalf("uncertain retirement lost holding: frames=%+v err=%v", frames, err)
	}
	groups.retireErr = nil
	restarted := &nativeHostHelperJournal{owner: nativeJournalFixture(j.owner.root), groups: groups}
	if err := restarted.retireAll(t.Context(), retiredOwner); err != nil {
		t.Fatal(err)
	}
	if err := restarted.requireRemoved(retiredOwner); err != nil {
		t.Fatal(err)
	}
	if err := j.owner.confirmResourcesRemoved(t.Context(), retiredOwner); err != nil {
		t.Fatal(err)
	}
	newOwner, err := j.owner.replace(t.Context(), owner.Lease, owner.Generation, true)
	if err != nil || newOwner.Generation == owner.Generation {
		t.Fatalf("replacement=%+v err=%v", newOwner, err)
	}
	if err := restarted.retire(t.Context(), owner, frame.Launch.Generation); err == nil {
		t.Fatal("old helper receipt borrowed replacement ownership")
	}
	if _, err := j.owner.records(t.Context()); err != nil {
		t.Fatal("retired helper lost immutable owner provenance:", err)
	}
}

func TestNativeHostHelperCorruptOrUnknownOwnershipCannotDisappear(t *testing.T) {
	for _, mutation := range []string{"missing_group", "duplicate_group", "null_group", "owner", "boot", "path", "missing_revocation", "unknown_generation", "untrusted_file", "trailing"} {
		t.Run(mutation, func(t *testing.T) {
			j, owner, _ := nativeHelperJournalFixture(t)
			frame := preparedNativeHelperFrame(t, j, owner)
			path := j.path(frame)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "missing_group":
				delete(fields, "group")
			case "duplicate_group":
				data = []byte(strings.TrimSpace(string(data)))
				data = append(data[:len(data)-1], []byte(`,"group":`+string(fields["group"])+`}`)...)
			case "null_group":
				fields["group"] = json.RawMessage("null")
			case "owner":
				fields["owner_generation"] = json.RawMessage(`"fd4dfbab-66b3-45f7-8cda-f5c4cd857681"`)
			case "boot", "path", "missing_revocation":
				key := "launch"
				if mutation == "path" {
					key = "group"
				}
				var nested map[string]json.RawMessage
				if err := json.Unmarshal(fields[key], &nested); err != nil {
					t.Fatal(err)
				}
				switch mutation {
				case "boot":
					nested["kernel_boot_id"] = json.RawMessage(`"fd4dfbab-66b3-45f7-8cda-f5c4cd857681"`)
				case "path":
					nested["path"] = json.RawMessage(`"../../unrelated"`)
				case "missing_revocation":
					delete(nested, "revoked")
				}
				fields[key], err = json.Marshal(nested)
				if err != nil {
					t.Fatal(err)
				}
			case "unknown_generation":
				unknown := filepath.Join(filepath.Dir(j.root(owner.Generation)), "fd4dfbab-66b3-45f7-8cda-f5c4cd857681")
				if err := os.Rename(j.root(owner.Generation), unknown); err != nil {
					t.Fatal(err)
				}
			case "untrusted_file":
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			case "trailing":
				data = append(data, []byte(`{}`)...)
			}
			if mutation != "unknown_generation" && mutation != "untrusted_file" {
				if mutation != "duplicate_group" && mutation != "trailing" {
					data, err = json.Marshal(fields)
					if err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := j.owner.records(t.Context()); err == nil {
				t.Fatal("corrupt or unknown helper ownership vanished from discovery")
			}
		})
	}
}

func TestNativeHostHelperWrapperRequiresLocalProducerAndNeverBypassesGate(t *testing.T) {
	_, v, _ := nativeManagerFixture(t)
	lease := leaseForSlot("host-helper-wrapper", 0)
	if err := v.prepareNativeLease(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	_, marker := nativeHelperFixtureCommand(t)
	v.nativeRecovery.helper = os.Args[0]
	v.nativeRecovery.startTime = func(int) (uint64, error) { return 1234, nil }
	if _, err := v.runHostHelper(t.Context(), lease.Instance, []string{os.Args[0]}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal(err)
	}
	owner, err := v.nativeRecovery.journal.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	j := nativeHostHelperJournal{owner: v.nativeRecovery.journal, groups: v.nativeRecovery.helperGroups}
	if err := j.requireRemoved(owner); err != nil {
		t.Fatal(err)
	}
	v.nativeRecovery.mu.Lock()
	delete(v.nativeRecovery.owned, lease.Instance)
	v.nativeRecovery.mu.Unlock()
	if _, err := v.runHostHelper(t.Context(), lease.Instance, []string{os.Args[0]}); err == nil {
		t.Fatal("restarted daemon borrowed old command producer")
	}
	v.nativeRecovery.remember(owner)
	if _, err := j.owner.revoke(t.Context(), lease.Instance); err != nil {
		t.Fatal(err)
	}
	if _, err := v.runHostHelper(t.Context(), lease.Instance, []string{os.Args[0]}); err == nil {
		t.Fatal("revoked command producer launched another helper")
	}
	cmd, err := newNativeHostHelperCommand(os.Args[0], []string{os.Args[0]})
	if err != nil {
		t.Fatal(err)
	}
	cmd.Args[1] = "--launch-jailer"
	if _, started, err := j.launch(t.Context(), owner, cmd); err == nil || started {
		t.Fatal("helper command substituted another launch protocol")
	}
}

func TestNativeHostHelperProducerLockSpansForkPublicationAndGate(t *testing.T) {
	j, owner, _ := nativeHelperJournalFixture(t)
	cmd, _ := nativeHelperFixtureCommand(t)
	entered, release := make(chan struct{}), make(chan struct{})
	j.startTime = func(int) (uint64, error) {
		close(entered)
		<-release
		return 1234, nil
	}
	done := make(chan error, 1)
	go func() { done <- j.run(t.Context(), owner, cmd, time.Second) }()
	<-entered
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	_, revokeErr := j.owner.revoke(ctx, owner.Lease.Instance)
	close(release)
	if !errors.Is(revokeErr, context.DeadlineExceeded) {
		t.Fatalf("revocation bypassed an in-flight producer: %v", revokeErr)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// A separate daemon fixture dies after fork but before PID publication. Its
// helper's inherited reader must observe EOF and refuse execution. Cgroups are
// modeled here; the native group test separately requires a dedicated host.
func TestNativeHostHelperDaemonDeathBeforePublicationClosesGate(t *testing.T) {
	j, owner, _ := nativeHelperJournalFixture(t)
	dir := t.TempDir()
	ready, marker, outcome := filepath.Join(dir, "ready"), filepath.Join(dir, "execution"), filepath.Join(dir, "gate-outcome")
	producer := exec.Command(os.Args[0], "-test.run=^TestNativeHostHelperProducerCrashFixture$")
	producer.Env = append(os.Environ(),
		"GREGALE_NATIVE_LAUNCH_FIXTURE=0", "GREGALE_NATIVE_PRODUCER_FIXTURE=1", "GREGALE_NATIVE_HELPER_PRODUCER_FIXTURE=1",
		"GREGALE_NATIVE_HELPER_JOURNAL="+j.owner.root, "GREGALE_NATIVE_HELPER_READY="+ready,
		"GREGALE_NATIVE_LAUNCH_MARKER="+marker, "GREGALE_NATIVE_GATE_OUTCOME="+outcome, "GORACE=atexit_sleep_ms=0")
	if err := producer.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = producer.Process.Kill() })
	waitNativeHelperFile(t, ready)
	if err := producer.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = producer.Wait()
	if data := waitNativeHelperFile(t, outcome); string(data) != "rejected" {
		t.Fatalf("orphan gate outcome=%q", data)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("daemon death permitted an unrecorded host command")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := j.owner.revoke(ctx, owner.Lease.Instance); err != nil {
		t.Fatal("orphan inherited the producer lock:", err)
	}
	frames, err := j.records(owner)
	if err != nil || len(frames) != 1 || frames[0].Launch.Authorized || frames[0].Group.Inode == 0 || frames[0].Launch.ResourcesRemoved {
		t.Fatalf("crash lost its durable holding: frames=%+v err=%v", frames, err)
	}
}

func TestNativeHostHelperProducerCrashFixture(t *testing.T) {
	if os.Getenv("GREGALE_NATIVE_HELPER_PRODUCER_FIXTURE") != "1" {
		return
	}
	ownerJournal := nativeJournalFixture(os.Getenv("GREGALE_NATIVE_HELPER_JOURNAL"))
	owner, err := ownerJournal.read("qualification-journal")
	if err != nil {
		t.Fatal(err)
	}
	j := &nativeHostHelperJournal{owner: ownerJournal, groups: newNativeHelperGroupsFixture()}
	j.startTime = func(pid int) (uint64, error) {
		if err := os.WriteFile(os.Getenv("GREGALE_NATIVE_HELPER_READY"), []byte(strconv.Itoa(pid)), 0o600); err != nil {
			return 0, err
		}
		for {
			time.Sleep(time.Hour)
		}
	}
	cmd, err := newNativeHostHelperCommand(os.Args[0], []string{os.Args[0]})
	if err != nil {
		t.Fatal(err)
	}
	cmd.Env = append(cmd.Env, "GREGALE_NATIVE_LAUNCH_FIXTURE=1")
	_, _, _ = j.launch(t.Context(), owner, cmd)
	t.Fatal("crash fixture unexpectedly returned")
}

func waitNativeHelperFile(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		if data, err := os.ReadFile(path); err == nil {
			return data
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case <-deadline.C:
			t.Fatalf("helper file %s never appeared", path)
		case <-time.After(time.Millisecond):
		}
	}
}
