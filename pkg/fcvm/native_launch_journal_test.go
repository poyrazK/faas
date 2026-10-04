//go:build linux || darwin

// adr: 567 — environment intent and runtime ownership contracts.
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func preparedNativeJournal(t *testing.T) (*nativeLaunchJournal, Lease) {
	t.Helper()
	j := nativeJournalFixture(filepath.Join(t.TempDir(), "journal"))
	lease := leaseForSlot("qualification-journal", 3)
	lease.Plan = api.PlanHobby
	if err := j.prepare(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	return j, lease
}

func nativeJournalFixture(root string) *nativeLaunchJournal {
	return &nativeLaunchJournal{root: root, bootID: func() (string, error) {
		return "d3124d9a-657b-4f37-a9df-f5dfb2e50764", nil
	}}
}

func TestNativeJournalRevocationFencesNewAndLateLaunches(t *testing.T) {
	j, lease := preparedNativeJournal(t)
	if err := j.prepare(t.Context(), lease); err == nil {
		t.Fatal("a second producer replaced an existing record")
	}
	changed := lease
	changed.Networkless = true
	if _, err := j.beginLaunch(t.Context(), changed); err == nil {
		t.Fatal("a changed lease borrowed the launch token")
	}
	if record, err := j.revoke(t.Context(), lease.Instance); err != nil || !record.Revoked || record.Authorized || record.ExitConfirmed {
		t.Fatalf("revoke prepared record=%+v err=%v", record, err)
	}
	if _, err := j.beginLaunch(t.Context(), lease); err == nil {
		t.Fatal("revocation permitted a late launch")
	}
	other := nativeJournalFixture(j.root)
	if record, err := other.read(lease.Instance); err != nil || !record.Revoked {
		t.Fatalf("restarted journal lost revocation: record=%+v err=%v", record, err)
	}
}

func TestNativeJournalKernelBootIdentityCannotBeReplayed(t *testing.T) {
	for _, outcome := range []string{"another_boot", "missing_boot", "invalid_boot", "nil_boot"} {
		t.Run(outcome, func(t *testing.T) {
			j, lease := preparedNativeJournal(t)
			before, err := os.ReadFile(j.path(lease.Instance))
			if err != nil {
				t.Fatal(err)
			}
			restarted := nativeJournalFixture(j.root)
			restarted.bootID = func() (string, error) {
				switch outcome {
				case "another_boot":
					return "de5b0c66-9c0d-4f4d-bb7d-d7fdcc6f7772", nil
				case "missing_boot":
					return "", os.ErrNotExist
				case "nil_boot":
					return "00000000-0000-0000-0000-000000000000", nil
				default:
					return "garbage", nil
				}
			}
			retirer := nativeProcessRetirer{open: func(int) (nativeProcessHandle, error) {
				t.Fatal("replayed kernel identity reached process retirement")
				return nil, nil
			}}
			if _, err := restarted.retire(t.Context(), lease.Instance, retirer); err == nil {
				t.Fatal("a replayed or unknown boot identity granted retirement authority")
			}
			if _, err := restarted.beginLaunch(t.Context(), lease); err == nil {
				t.Fatal("replayed kernel identity granted launch authority")
			}
			after, err := os.ReadFile(j.path(lease.Instance))
			if err != nil || string(before) != string(after) {
				t.Fatalf("replayed identity mutated the record: err=%v", err)
			}
		})
	}
}

func TestNativeJournalExitConfirmationCannotBorrowAnotherIncarnation(t *testing.T) {
	for _, changed := range []string{"generation", "kernel_boot", "pid", "start_time", "lease"} {
		t.Run(changed, func(t *testing.T) {
			j, lease := preparedNativeJournal(t)
			record, err := j.revoke(t.Context(), lease.Instance)
			if err != nil {
				t.Fatal(err)
			}
			switch changed {
			case "generation":
				record.Generation = "de5b0c66-9c0d-4f4d-bb7d-d7fdcc6f7772"
			case "kernel_boot":
				record.KernelBootID = "de5b0c66-9c0d-4f4d-bb7d-d7fdcc6f7772"
			case "pid":
				record.PID++
			case "start_time":
				record.StartTime++
			case "lease":
				record.Lease.Networkless = true
			}
			if err := j.confirmExit(t.Context(), record); err == nil {
				t.Fatal("another incarnation borrowed an exit acknowledgement")
			}
			persisted, err := j.read(lease.Instance)
			if err != nil || persisted.ExitConfirmed {
				t.Fatalf("mismatched confirmation persisted: record=%+v err=%v", persisted, err)
			}
		})
	}
}

func TestNativeJournalProducerLockSpansAuthorizationAndBoundsRevocation(t *testing.T) {
	j, lease := preparedNativeJournal(t)
	ticket, err := j.beginLaunch(t.Context(), lease)
	if err != nil {
		t.Fatal(err)
	}
	defer ticket.close()
	other := nativeJournalFixture(j.root)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, err := other.revoke(ctx, lease.Instance); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("revocation raced a live producer: %v", err)
	}
	if err := ticket.authorize(42, 101); err != nil {
		t.Fatal(err)
	}
	if err := ticket.authorize(43, 102); err == nil {
		t.Fatal("one ticket authorized two incarnations")
	}
	if err := ticket.close(); err != nil {
		t.Fatal(err)
	}
	record, err := other.revoke(t.Context(), lease.Instance)
	if err != nil || !record.Revoked || !record.Authorized || record.PID != 42 || record.StartTime != 101 {
		t.Fatalf("revoke after producer release: record=%+v err=%v", record, err)
	}
	if err := ticket.authorize(44, 103); err == nil {
		t.Fatal("closed ticket authorized a late incarnation")
	}
}

func nativeJournalFixtureCommand(t *testing.T, j *nativeLaunchJournal, lease Lease) *exec.Cmd {
	t.Helper()
	args := JailerCommand(JailerSpec{Instance: lease.Instance, UID: lease.UID, GID: lease.GID, Netns: lease.Netns, Plan: lease.Plan, IsBuilder: lease.IsBuilder, Networkless: lease.Networkless, ExecFile: "/usr/bin/firecracker-v1.7.0"})
	args[0] = "/usr/bin/jailer-v1.7.0"
	for i, arg := range args {
		if arg == "--chroot-base-dir" {
			args[i+1] = filepath.Dir(j.root)
		}
	}
	cmd, err := newNativeLaunchCommand(os.Args[0], args)
	if err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestNativeJournalProducerCrashFixture(t *testing.T) {
	if os.Getenv("GREGALE_NATIVE_PRODUCER_FIXTURE") != "1" {
		return
	}
	j := nativeJournalFixture(os.Getenv("GREGALE_NATIVE_JOURNAL_ROOT"))
	lease := leaseForSlot("qualification-journal", 3)
	lease.Plan = api.PlanHobby
	child := nativeJournalFixtureCommand(t, j, lease)
	child.Env = append(os.Environ(), "GREGALE_NATIVE_LAUNCH_FIXTURE=1")
	_, _ = j.launch(t.Context(), lease, child, func(pid int) (uint64, error) {
		if err := os.WriteFile(os.Getenv("GREGALE_NATIVE_CHILD_PID"), []byte(strconv.Itoa(pid)), 0o600); err != nil {
			os.Exit(4)
		}
		// Die here with an open producer lock and an unrecorded child.
		for {
			time.Sleep(time.Hour)
		}
	})
	os.Exit(5)
}

func TestNativeJournalProducerDeathClosesUnrecordedChildGateAndReleasesLock(t *testing.T) {
	j, lease := preparedNativeJournal(t)
	root := t.TempDir()
	pidFile, marker, outcome := filepath.Join(root, "child-pid"), filepath.Join(root, "execution"), filepath.Join(root, "gate-outcome")
	producer := exec.Command(os.Args[0], "-test.run=^TestNativeJournalProducerCrashFixture$")
	producer.Env = append(os.Environ(), "GORACE=atexit_sleep_ms=0", "GREGALE_NATIVE_PRODUCER_FIXTURE=1", "GREGALE_NATIVE_JOURNAL_ROOT="+j.root,
		"GREGALE_NATIVE_CHILD_PID="+pidFile, "GREGALE_NATIVE_LAUNCH_MARKER="+marker, "GREGALE_NATIVE_GATE_OUTCOME="+outcome)
	if err := producer.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = producer.Process.Kill() })
	wait := make(chan error, 1)
	go func() { wait <- producer.Wait() }()
	waitForNativeFixtureFile(t, pidFile)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, err := nativeJournalFixture(j.root).revoke(ctx, lease.Instance); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("recovery bypassed another process's live launch lock: %v", err)
	}
	if err := producer.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-wait:
	case <-time.After(3 * time.Second):
		t.Fatal("producer did not exit")
	}
	retirer := nativeProcessRetirer{probe: nativeProcessProbe{root: t.TempDir()}}
	record, err := nativeJournalFixture(j.root).retire(t.Context(), lease.Instance, retirer)
	if err != nil || record.Authorized || !record.Revoked || !record.ExitConfirmed {
		t.Fatalf("crash recovery record=%+v err=%v", record, err)
	}
	waitForNativeFixtureFile(t, outcome)
	if raw, err := os.ReadFile(outcome); err != nil || string(raw) != "rejected" {
		t.Fatalf("orphan child gate outcome=%q err=%v", raw, err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unrecorded child executed after producer death")
	}
}

func waitForNativeFixtureFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("fixture did not create %s", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestNativeJournalLaunchNeverOpensGateBeforeIncarnationPersistence(t *testing.T) {
	for _, outcome := range []string{"success", "write_failure", "identity_failure", "cancel_after_fork", "revoked", "start_failure"} {
		t.Run(outcome, func(t *testing.T) {
			j, lease := preparedNativeJournal(t)
			marker := filepath.Join(t.TempDir(), "execution-marker")
			cmd := nativeJournalFixtureCommand(t, j, lease)
			cmd.Env = append(os.Environ(), "GORACE=atexit_sleep_ms=0", "GREGALE_NATIVE_LAUNCH_FIXTURE=1", "GREGALE_NATIVE_LAUNCH_MARKER="+marker)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cause := errors.New("incarnation persistence failed")
			if outcome == "write_failure" {
				j.writeRecord = func(string, nativeLaunchRecord) error { return cause }
			}
			if outcome == "revoked" {
				if _, err := j.revoke(t.Context(), lease.Instance); err != nil {
					t.Fatal(err)
				}
			}
			if outcome == "start_failure" {
				cmd.Path = filepath.Join(t.TempDir(), "missing-helper")
			}
			startTime := func(pid int) (uint64, error) {
				if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("helper executed before incarnation publication")
				}
				if outcome == "identity_failure" {
					return 0, cause
				}
				if outcome == "cancel_after_fork" {
					cancel()
				}
				return 101, nil
			}
			started, err := j.launch(ctx, lease, cmd, startTime)
			if started {
				t.Cleanup(func() { _ = cmd.Process.Kill() })
				wait := make(chan error, 1)
				go func() { wait <- cmd.Wait() }()
				select {
				case waitErr := <-wait:
					if outcome == "success" && waitErr != nil || outcome != "success" && waitErr == nil {
						t.Fatalf("helper exit=%v outcome=%s", waitErr, outcome)
					}
				case <-time.After(3 * time.Second):
					_ = cmd.Process.Kill()
					<-wait
					t.Fatal("helper remained alive after its gate closed")
				}
			}
			if outcome == "success" {
				if !started || err != nil {
					t.Fatalf("successful launch started=%v err=%v", started, err)
				}
				record, readErr := j.read(lease.Instance)
				if readErr != nil || !record.Authorized || record.PID != cmd.Process.Pid || record.StartTime != 101 {
					t.Fatalf("launch record=%+v err=%v", record, readErr)
				}
				if _, err := os.Stat(marker); err != nil {
					t.Fatalf("authorized helper did not execute: %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("failed launch returned success")
				}
				if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("helper executed after failed launch")
				}
			}
		})
	}
}

func TestNativeJournalRetirementRequiresRevokedProducerAndEveryTaskGone(t *testing.T) {
	for _, outcome := range []string{"prepared", "authorized", "wait_failure", "scan_failure", "duplicate", "unknown"} {
		t.Run(outcome, func(t *testing.T) {
			j, lease := preparedNativeJournal(t)
			root := t.TempDir()
			h := &recoveryHandle{}
			if outcome != "prepared" && outcome != "unknown" {
				ticket, err := j.beginLaunch(t.Context(), lease)
				if err != nil {
					t.Fatal(err)
				}
				if err := ticket.authorize(42, 101); err != nil {
					t.Fatal(err)
				}
				_ = ticket.close()
				dir := writeNativeRecoveryProcess(t, root, 42, lease.Instance, lease.UID, 101)
				h.onWait = func(context.Context) error { return os.RemoveAll(dir) }
			}
			if outcome == "wait_failure" {
				h.onWait = func(context.Context) error { return errors.New("native wait uncertain") }
			}
			if outcome == "scan_failure" {
				h.onWait = func(context.Context) error { return os.RemoveAll(root) }
			}
			if outcome == "duplicate" {
				writeNativeRecoveryProcess(t, root, 43, lease.Instance, lease.UID+1, 102)
			}
			if outcome == "unknown" {
				if err := os.Remove(j.path(lease.Instance)); err != nil {
					t.Fatal(err)
				}
			}
			r := nativeProcessRetirer{probe: nativeProcessProbe{root: root}, open: func(int) (nativeProcessHandle, error) { return h, nil }}
			record, err := j.retire(t.Context(), lease.Instance, r)
			success := outcome == "prepared" || outcome == "authorized"
			if success != (err == nil) || record.ExitConfirmed != success {
				t.Fatalf("retirement record=%+v err=%v, want success=%v", record, err, success)
			}
			if outcome != "unknown" {
				persisted, readErr := j.read(lease.Instance)
				if readErr != nil || !persisted.Revoked || persisted.ExitConfirmed != success {
					t.Fatalf("persisted record=%+v err=%v", persisted, readErr)
				}
			}
		})
	}
}

func TestNativeJournalRejectsCorruptOrUntrustedRecords(t *testing.T) {
	for _, corrupt := range []string{"generation", "nil_generation", "pid", "uid", "instance", "exit_without_revoke", "world_readable", "symlink", "trailing", "unknown_field", "duplicate_field", "duplicate_lease_field", "missing_revoke", "missing_lease_field", "null_field"} {
		t.Run(corrupt, func(t *testing.T) {
			j, lease := preparedNativeJournal(t)
			record, err := j.read(lease.Instance)
			if err != nil {
				t.Fatal(err)
			}
			path := j.path(lease.Instance)
			switch corrupt {
			case "generation":
				record.Generation = "garbage"
			case "nil_generation":
				record.Generation = "00000000-0000-0000-0000-000000000000"
			case "pid":
				record.Authorized = true
			case "uid":
				record.Lease.UID++
			case "instance":
				record.Lease.Instance = "other"
			case "exit_without_revoke":
				record.ExitConfirmed = true
			case "world_readable":
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(path, path+"-other"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+"-other", path); err != nil {
					t.Fatal(err)
				}
			}
			if corrupt != "world_readable" && corrupt != "symlink" {
				raw, err := json.Marshal(record)
				if err != nil {
					t.Fatal(err)
				}
				if corrupt == "trailing" {
					raw = append(raw, []byte("{}")...)
				}
				if corrupt == "unknown_field" {
					raw = append(raw[:len(raw)-1], []byte(",\"future\":true}")...)
				}
				if corrupt == "duplicate_field" {
					raw = append(raw[:len(raw)-1], []byte(",\"revoked\":false}")...)
				}
				if corrupt == "duplicate_lease_field" {
					raw = []byte(strings.Replace(string(raw), "\"UID\":20003", "\"UID\":20003,\"UID\":20004", 1))
				}
				if corrupt == "missing_revoke" {
					raw = []byte(strings.Replace(string(raw), "\"revoked\":false,", "", 1))
				}
				if corrupt == "missing_lease_field" {
					raw = []byte(strings.Replace(string(raw), ",\"Networkless\":false", "", 1))
				}
				if corrupt == "null_field" {
					raw = []byte(strings.Replace(string(raw), "\"revoked\":false", "\"revoked\":null", 1))
				}
				if err := os.WriteFile(path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := j.read(lease.Instance); err == nil {
				t.Fatal("untrusted record passed validation")
			}
		})
	}
}

func TestNativeJournalRefusesCommandsThatCanBypassItsGate(t *testing.T) {
	for _, corrupt := range []string{"direct", "uid", "parent", "instance", "namespace", "daemonize", "new_pid_namespace", "cgroup_v1"} {
		t.Run(corrupt, func(t *testing.T) {
			j, lease := preparedNativeJournal(t)
			cmd := nativeJournalFixtureCommand(t, j, lease)
			switch corrupt {
			case "direct":
				cmd.Args = cmd.Args[4:]
			case "daemonize":
				cmd.Args = append(cmd.Args, "--daemonize")
			case "new_pid_namespace":
				cmd.Args = append(cmd.Args, "--new-pid-ns")
			default:
				flag, value := "", ""
				switch corrupt {
				case "uid":
					flag, value = "--uid", "20004"
				case "parent":
					flag, value = "--parent-cgroup", "user.slice"
				case "instance":
					flag, value = "--id", "another-instance"
				case "namespace":
					flag, value = "--netns", "/run/netns/fc-another-instance"
				case "cgroup_v1":
					flag, value = "--cgroup-version", "1"
				}
				for i, arg := range cmd.Args {
					if arg == flag {
						cmd.Args[i+1] = value
					}
				}
			}
			if started, err := j.launch(t.Context(), lease, cmd, nil); started || err == nil || cmd.Process != nil {
				t.Fatalf("invalid launch started=%v pid=%v err=%v", started, cmd.Process, err)
			}
			record, err := j.read(lease.Instance)
			if err != nil || record.Authorized || record.Revoked {
				t.Fatalf("invalid command mutated launch record: record=%+v err=%v", record, err)
			}
		})
	}
}

func TestNativePhysicalLeaseReopenExcludesOnlyCallbackGeneration(t *testing.T) {
	j := nativeJournalFixture(filepath.Join(t.TempDir(), "journal"))
	lease := leaseForSlot("qualification-callback", 3)
	lease.Plan, lease.MemoryMaxMiB, lease.CPUMillicores, lease.BuildTimeoutSec = api.PlanHobby, 256, 500, 60
	lease.processGeneration = 19
	if err := j.prepare(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	reopened := nativeJournalFixture(j.root)
	record, err := reopened.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	if record.Lease.processGeneration != 0 || !sameNativePhysicalLease(record.Lease, lease) {
		t.Fatal("private callback fence changed durable lease ownership")
	}
	for _, mutate := range []func(*Lease){
		func(l *Lease) { l.MemoryMaxMiB++ },
		func(l *Lease) { l.CPUMillicores++ },
		func(l *Lease) { l.BuildTimeoutSec++ },
		func(l *Lease) { l.DisableStartupCPUBoost = !l.DisableStartupCPUBoost },
		func(l *Lease) { l.Networkless = !l.Networkless },
		func(l *Lease) { l.UID++ },
	} {
		changed := lease
		mutate(&changed)
		if sameNativePhysicalLease(record.Lease, changed) {
			t.Fatal("a changed durable field borrowed original ownership")
		}
	}
}
