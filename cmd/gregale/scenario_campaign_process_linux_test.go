//go:build linux

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCampaignRecoveryCoordinatorWorker(t *testing.T) {
	if os.Getenv("GREGALE_TEST_COORDINATOR") != "1" {
		return
	}
	directory := os.Getenv("GREGALE_TEST_PROCESS_DIRECTORY")
	var plan testCampaignPolicyPlan
	var cp testCampaignCheckpoint
	if err := readTestCampaignJSON(os.Getenv("GREGALE_TEST_LOCK_PLAN"), &plan); err != nil {
		t.Fatal(err)
	}
	if err := readTestCampaignJSON(filepath.Join(directory, "checkpoint.json"), &cp); err != nil {
		t.Fatal(err)
	}
	release, err := acquireTestCampaignRunnerLock(directory, testCampaignJSONHash(plan))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	batch := plan.Batches[1]
	cp.Batches[batch.ID] = testCampaignCheckpointBatch{Status: "running"}
	if err := persistTestCampaignProcess(filepath.Join(directory, "checkpoint.json"), cp); err != nil {
		t.Fatal(err)
	}
	journal, err := prepareTestCampaignJournal(directory, batch)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(context.Background(), executable, "-test.run=^TestCampaignRecoveryProcessWorker$", "-test.timeout=60s")
	command.Env = append(os.Environ(), "GREGALE_TEST_PROCESS_WORKER=1", testCampaignJournalEnv+"="+journal)
	command.Stdout, command.Stderr = os.Stderr, os.Stderr
	configureTestCampaignWorker(command)
	if err := runTestCampaignTrackedWorker(command, directory, batch.ID); err != nil {
		t.Fatal(err)
	}
}

func TestCampaignRecoveryCoordinatorCrash(t *testing.T) {
	// adr: 984 — killing the coordinator never authorizes stealing a live worker's lock or repeating uncertain work.
	for _, mode := range []string{"worker_alive", "worker_killed"} {
		t.Run(mode, func(t *testing.T) {
			stub := newCampaignRecoveryAcceptanceAPI(t)
			f := newCampaignStatusTestFiles(t)
			if err := writeTestCampaignJSON(filepath.Join(f.directory, "process-scenario.json"), f.scenarios["checkout"]); err != nil {
				t.Fatal(err)
			}
			stub.mu.Lock()
			stub.sessions[strings.Repeat("b", 32)] = "app-b"
			stub.mu.Unlock()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			command := exec.Command(executable, "-test.run=^TestCampaignRecoveryCoordinatorWorker$", "-test.timeout=60s")
			command.Env = append(os.Environ(), "GREGALE_TEST_COORDINATOR=1",
				"GREGALE_TEST_PROCESS_DIRECTORY="+f.directory, "GREGALE_TEST_LOCK_PLAN="+f.planPath)
			command.Stdout, command.Stderr = os.Stderr, os.Stderr
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = command.Process.Kill() })
			waitProcessRecoveryFile(t, filepath.Join(f.directory, "ready"))
			var lock testCampaignRunnerLock
			if err := readTestCampaignJSON(filepath.Join(f.directory, "runner.lock"), &lock); err != nil {
				t.Fatal(err)
			}
			var record testCampaignWorkerProcess
			path := filepath.Join(testCampaignProcessDirectory(f.directory, lock), "b.json")
			deadline := time.Now().Add(10 * time.Second)
			for {
				if err := readTestCampaignJSON(path, &record); err == nil && record.State == "started" && record.Process != nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("worker identity was not persisted")
				}
				time.Sleep(10 * time.Millisecond)
			}
			worker, err := os.FindProcess(record.Process.PID)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if status, _ := probeTestCampaignProcess(*record.Process); status == "alive" || status == "stopped" {
					_ = worker.Kill()
				}
				_ = worker.Release()
			})
			args := []string{"recover-lock", "--plan", f.planPath, "--directory", f.directory}
			before, err := os.ReadFile(filepath.Join(f.directory, "checkpoint.json"))
			if err != nil {
				t.Fatal(err)
			}
			if code := cmdTestPolicy(append(append([]string{}, args...), "--execute")); code != 2 {
				t.Fatalf("live coordinator lock recovery returned %d", code)
			}
			if err := command.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			if err := command.Wait(); err == nil {
				t.Fatal("coordinator unexpectedly exited successfully")
			}
			var cp testCampaignCheckpoint
			if err := readTestCampaignJSON(filepath.Join(f.directory, "checkpoint.json"), &cp); err != nil {
				t.Fatal(err)
			}
			assessment, err := inspectTestCampaignRunnerLock(f.directory, f.plan, cp)
			if err != nil || assessment.Recoverable || assessment.Coordinator.Status != "terminated" ||
				len(assessment.Workers) != 1 || assessment.Workers[0].Status != "alive" {
				t.Fatalf("orphan worker was not protected: %+v %v", assessment, err)
			}
			if code := cmdTestPolicy(append(append([]string{}, args...), "--execute")); code != 2 {
				t.Fatalf("live orphan worker lock recovery returned %d", code)
			}
			after, _ := os.ReadFile(filepath.Join(f.directory, "checkpoint.json"))
			if string(before) != string(after) {
				t.Fatal("blocked recovery changed checkpoint")
			}
			if mode == "worker_alive" {
				if err := worker.Signal(syscall.SIGSTOP); err != nil {
					t.Fatal(err)
				}
				deadline = time.Now().Add(5 * time.Second)
				for {
					state, _ := probeTestCampaignProcess(*record.Process)
					if state == "stopped" {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("worker did not stop")
					}
					time.Sleep(10 * time.Millisecond)
				}
				if code := cmdTestPolicy(append(append([]string{}, args...), "--execute")); code != 2 {
					t.Fatal("stopped worker permitted lock recovery")
				}
				if err := worker.Signal(syscall.SIGCONT); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "worker_killed" {
				if err := worker.Kill(); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := worker.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
				waitProcessRecoveryFile(t, filepath.Join(f.directory, "cleanup-started"))
				processRecoveryMarker(t, f.directory, "release-cleanup")
				waitProcessRecoveryFile(t, filepath.Join(f.directory, "cleanup-finished"))
			}
			deadline = time.Now().Add(10 * time.Second)
			for {
				status, _ := probeTestCampaignProcess(*record.Process)
				if status == "terminated" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("worker termination was not observed")
				}
				time.Sleep(10 * time.Millisecond)
			}
			// Default command is read-only, even when recovery is possible.
			if code := cmdTestPolicy(args); code != 0 {
				t.Fatalf("inspection failed: %d", code)
			}
			if _, err := os.Stat(filepath.Join(f.directory, "runner.lock")); err != nil {
				t.Fatal("inspection removed lock")
			}
			if code := cmdTestPolicy(append(append([]string{}, args...), "--execute")); code != 0 {
				t.Fatalf("terminated processes could not release lock: %d", code)
			}
			if _, err := os.Lstat(filepath.Join(f.directory, "runner.lock")); !os.IsNotExist(err) {
				t.Fatal("stale lock was not removed")
			}
			if err := readTestCampaignJSON(filepath.Join(f.directory, "checkpoint.json"), &cp); err != nil {
				t.Fatal(err)
			}
			if !cp.Halted || cp.Batches["b"].Status != "uncertain" || cp.Batches["a"] != f.checkpoint.Batches["a"] {
				t.Fatalf("recovery altered completed evidence or lost uncertainty: %+v", cp)
			}
			launches := 0
			if err := runTestCampaignBatches(context.Background(), f.plan, f.scenarios, f.directory, &cp, 1,
				func(testCampaignCostBatch) error { return nil },
				func(context.Context, testCampaignCostBatch, string) error { launches++; return nil }); err == nil || launches != 0 {
				t.Fatal("unlocked campaign repeated uncertain work")
			}
			_, deletes := stub.counts()
			if mode == "worker_killed" {
				if deletes != 0 {
					t.Fatal("lock recovery performed resource deletion")
				}
				if code := cmdTestCampaignControl("cleanup", []string{"--plan", f.planPath, "--policy", f.policyPath,
					"--manifest", f.manifestPath, "--directory", f.directory, "--batch", "b", "--journal", "--execute"}); code != 0 {
					t.Fatalf("journal cleanup after lock recovery failed: %d", code)
				}
			} else if deletes != 1 {
				t.Fatalf("orphan worker cleanup was not drained: %d", deletes)
			}
			if _, err := os.Stat(filepath.Join(f.directory, "batch-b.json")); !os.IsNotExist(err) {
				t.Fatal("lock recovery manufactured execution evidence")
			}
		})
	}
}

func TestCampaignRecoveryProcessIdentityGuards(t *testing.T) {
	// adr: 984 — PID reuse, foreign domains and legacy/startup records cannot be mistaken for safe live ownership.
	identity, err := readTestCampaignProcessIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := probeTestCampaignProcess(identity); status != "alive" {
		t.Fatal("current process identity is not alive")
	}
	for _, mode := range []string{"foreign_boot", "foreign_namespace", "missing_start", "invalid_pid"} {
		copy := identity
		switch mode {
		case "foreign_boot":
			copy.BootID = "different-host"
		case "foreign_namespace":
			copy.Namespace = "pid:[different]"
		case "missing_start":
			copy.Start = ""
		case "invalid_pid":
			copy.PID = -1
		}
		if status, _ := probeTestCampaignProcess(copy); status != "unverifiable" {
			t.Fatalf("%s was treated as verified: %s", mode, status)
		}
	}
	start, _ := strconv.ParseUint(identity.Start, 10, 64)
	reused := identity
	reused.Start = strconv.FormatUint(start+1, 10)
	if status, _ := probeTestCampaignProcess(reused); status != "terminated" {
		t.Fatal("PID reuse was not distinguished from the recorded process")
	}
	f := newCampaignStatusTestFiles(t)
	release, err := acquireTestCampaignRunnerLock(f.directory, testCampaignJSONHash(f.plan))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	var lock testCampaignRunnerLock
	if err := readTestCampaignJSON(filepath.Join(f.directory, "runner.lock"), &lock); err != nil {
		t.Fatal(err)
	}
	lock.Coordinator = reused
	if err := writeTestCampaignJSON(filepath.Join(f.directory, "runner.lock"), lock); err != nil {
		t.Fatal(err)
	}
	f.checkpoint.Batches["b"] = testCampaignCheckpointBatch{Status: "running"}
	if err := writeTestCampaignJSON(filepath.Join(f.directory, "checkpoint.json"), f.checkpoint); err != nil {
		t.Fatal(err)
	}
	result, err := releaseTestCampaignRunnerLock(f.directory, f.plan)
	if err != nil || result.Recoverable || len(result.Workers) != 1 || result.Workers[0].Status != "unverifiable" {
		t.Fatalf("missing worker record permitted recovery: %+v %v", result, err)
	}
	dir := testCampaignProcessDirectory(f.directory, lock)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	record := testCampaignWorkerProcess{SchemaVersion: 1, LockSHA256: testCampaignJSONHash(lock), BatchID: "b", State: "starting"}
	if err := writeTestCampaignJSON(filepath.Join(dir, "b.json"), record); err != nil {
		t.Fatal(err)
	}
	result, err = releaseTestCampaignRunnerLock(f.directory, f.plan)
	if err != nil || result.Recoverable || result.Workers[0].Status != "unverifiable" {
		t.Fatalf("PID-less startup window permitted recovery: %+v %v", result, err)
	}
	if err := os.WriteFile(filepath.Join(f.directory, "runner.lock"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err = releaseTestCampaignRunnerLock(f.directory, f.plan)
	if err != nil || result.Recoverable || result.Coordinator.Status != "unverifiable" {
		t.Fatalf("legacy lock permitted recovery: %+v %v", result, err)
	}
}
