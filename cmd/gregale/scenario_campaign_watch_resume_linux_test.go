//go:build linux

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestCampaignCleanupWatchReceiptProcess(t *testing.T) {
	// adr: 984 — real identities used by receipt recovery tests never run without an explicit child role.
	if os.Getenv("GREGALE_WATCH_RECEIPT_PROCESS") != "1" {
		return
	}
	<-time.After(time.Minute)
}

func campaignWatchReceiptProcess(t *testing.T) (*exec.Cmd, testCampaignProcessIdentity) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-test.run=^TestCampaignCleanupWatchReceiptProcess$", "-test.timeout=70s")
	cmd.Env = append(os.Environ(), "GREGALE_WATCH_RECEIPT_PROCESS=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	identity, err := readTestCampaignProcessIdentity(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	return cmd, identity
}

func killCampaignWatchReceiptProcess(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	var exited *exec.ExitError
	if !errors.As(cmd.Wait(), &exited) {
		t.Fatal("receipt owner was not killed")
	}
	status, ok := exited.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatal("receipt owner death was not verified")
	}
}

func TestCampaignCleanupWatchDurableResume(t *testing.T) {
	// adr: 984 — takeover survives repeated process deaths, while active/foreign ownership stays protected.
	for _, mode := range []string{"between_attempts", "stale_runner", "foreign_owner", "live_worker", "dead_worker", "successive_takeover", "takeover_interrupted", "changed_checkpoint", "missing_receipt"} {
		t.Run(mode, func(t *testing.T) {
			w, f := newCampaignWatchReceiptFixture(t)
			owner, identity := campaignWatchReceiptProcess(t)
			killCampaignWatchReceiptProcess(t, owner)
			w.Receipt.Owner = identity
			if err := w.persist(); err != nil {
				t.Fatal(err)
			}
			if mode == "changed_checkpoint" {
				cp := f.cp
				cp.Halted = false
				mustCampaignFaultJSON(t, filepath.Join(w.Paths.Directory, "checkpoint.json"), cp)
			}
			if mode == "missing_receipt" {
				if err := os.Remove(filepath.Join(w.Paths.Directory, testCampaignWatchReceiptName)); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "stale_runner" || mode == "foreign_owner" || mode == "live_worker" || mode == "dead_worker" || mode == "takeover_interrupted" {
				release, err := acquireTestCampaignRunnerLock(w.Paths.Directory, testCampaignJSONHash(f.plan))
				if err != nil {
					t.Fatal(err)
				}
				defer release()
				var lock testCampaignRunnerLock
				if err := readTestCampaignJSON(filepath.Join(w.Paths.Directory, "runner.lock"), &lock); err != nil {
					t.Fatal(err)
				}
				lock.Coordinator = identity
				if mode == "foreign_owner" {
					foreign, id := campaignWatchReceiptProcess(t)
					killCampaignWatchReceiptProcess(t, foreign)
					lock.Coordinator = id
				}
				mustCampaignFaultJSON(t, filepath.Join(w.Paths.Directory, "runner.lock"), lock)
				if mode == "live_worker" || mode == "dead_worker" {
					worker, id := campaignWatchReceiptProcess(t)
					if mode == "dead_worker" {
						killCampaignWatchReceiptProcess(t, worker)
					} else {
						defer killCampaignWatchReceiptProcess(t, worker)
					}
					dir := testCampaignProcessDirectory(w.Paths.Directory, lock)
					if err := os.Mkdir(dir, 0700); err != nil {
						t.Fatal(err)
					}
					mustCampaignFaultJSON(t, filepath.Join(dir, "a.json"), testCampaignWorkerProcess{SchemaVersion: 1, LockSHA256: testCampaignJSONHash(lock), BatchID: "a", State: "started", Process: &id})
				}
			}
			if mode == "successive_takeover" || mode == "takeover_interrupted" {
				successor, id := campaignWatchReceiptProcess(t)
				killCampaignWatchReceiptProcess(t, successor)
				w.Receipt.PreviousOwners = append(w.Receipt.PreviousOwners, identity)
				w.Receipt.Owner = id
				if err := w.persist(); err != nil {
					t.Fatal(err)
				}
			}
			err := w.resume()
			allowed := mode == "between_attempts" || mode == "stale_runner" || mode == "successive_takeover" || mode == "takeover_interrupted"
			if !allowed {
				if err == nil {
					t.Fatal("unsafe durable takeover was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if w.Receipt.Owner.PID != os.Getpid() || len(w.Receipt.PreviousOwners) == 0 {
				t.Fatal("successor identity was not persisted")
			}
			if err := w.validate(false); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(filepath.Join(w.Paths.Directory, "runner.lock")); !os.IsNotExist(err) {
				t.Fatal("stale exclusion survived verified recovery")
			}
			if _, err := w.resumeStamp(true); err == nil {
				t.Fatal("active successor was eligible for another takeover")
			}
		})
	}
}
