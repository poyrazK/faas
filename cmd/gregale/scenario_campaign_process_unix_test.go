//go:build unix

package main

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Test binary subprocess: real signals and durable journal writes, with no VM.
// The production launcher and this worker share the cancellation/drain path.
func TestCampaignRecoveryProcessWorker(t *testing.T) {
	if os.Getenv("GREGALE_TEST_PROCESS_WORKER") != "1" {
		return
	}
	directory := os.Getenv("GREGALE_TEST_PROCESS_DIRECTORY")
	var scenario testScenario
	if err := readTestCampaignJSON(filepath.Join(directory, "process-scenario.json"), &scenario); err != nil {
		t.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), testTerminationSignals()...)
	defer stop()
	writer, err := newTestCampaignRunJournal(os.Getenv(testCampaignJournalEnv), "checkout", "warm", strings.Repeat("b", 32), scenario)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"creating", "provisioned"} {
		id := ""
		if state == "provisioned" {
			id = "app-b"
		}
		if err := writer.update("checkout", id, state); err != nil {
			t.Fatal(err)
		}
	}
	processRecoveryMarker(t, directory, "ready")
	<-ctx.Done()
	if err := writer.update("checkout", "app-b", "cleanup_started"); err != nil {
		t.Fatal(err)
	}
	processRecoveryMarker(t, directory, "cleanup-started")
	waitProcessRecoveryFile(t, filepath.Join(directory, "release-cleanup"))
	client, err := authedClient()
	if err != nil {
		t.Fatal(err)
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	targets := map[string]testCampaignCleanupAction{"checkout": {
		Project: scenario.Project, RunID: strings.Repeat("b", 32), AppID: "app-b",
	}}
	if _, err := testCampaignRetryCleanupTargets(cleanup, client, targets, true); err != nil {
		t.Fatal(err)
	}
	if err := writer.update("checkout", "app-b", "deleted"); err != nil {
		t.Fatal(err)
	}
	processRecoveryMarker(t, directory, "cleanup-finished")
	// Deliberately no final report: cleanup does not prove test completion.
}

func processRecoveryMarker(t *testing.T, directory, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func waitProcessRecoveryFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for subprocess marker %s", path)
		case <-ticker.C:
		}
	}
}

func TestCampaignRecoveryProcessInterruption(t *testing.T) {
	// adr: 984 — actual process loss preserves cleanup identities and forbids implicit retries.
	for _, mode := range []string{"context_interrupt", "sigterm", "kill"} {
		t.Run(mode, func(t *testing.T) {
			stub := newCampaignRecoveryAcceptanceAPI(t)
			f := newCampaignStatusTestFiles(t)
			receipt := campaignRecoveryTestReceipt(f.scenarios["checkout"], strings.Repeat("a", 32))
			receipt.WorkloadAppIDs = map[string]string{"checkout": "app-a"}
			report := filepath.Join(f.directory, "batch-a.json")
			if err := writeTestCampaignJSON(report, []testRunReceipt{receipt}); err != nil {
				t.Fatal(err)
			}
			_, hash, err := readTestBaselineReport(report, nil)
			if err != nil {
				t.Fatal(err)
			}
			f.checkpoint.Batches["a"] = testCampaignCheckpointBatch{Status: "done", ReportSHA256: hash}
			next := f.plan.Batches[len(f.plan.Batches)-1]
			next.ID = "c"
			f.plan.Batches = append(f.plan.Batches, next)
			f.checkpoint.PlanSHA256 = testCampaignJSONHash(f.plan)
			if err := writeTestCampaignJSON(filepath.Join(f.directory, "process-scenario.json"), f.scenarios["checkout"]); err != nil {
				t.Fatal(err)
			}
			stub.mu.Lock()
			stub.sessions[strings.Repeat("b", 32)] = "app-b"
			stub.mu.Unlock()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started := make(chan *exec.Cmd, 1)
			done := make(chan error, 1)
			go func() {
				done <- runTestCampaignBatches(ctx, f.plan, f.scenarios, f.directory, &f.checkpoint, 1,
					func(testCampaignCostBatch) error { return nil },
					func(ctx context.Context, batch testCampaignCostBatch, directory string) error {
						if batch.ID != "b" {
							return syscall.EINVAL
						}
						journal, err := prepareTestCampaignJournal(directory, batch)
						if err != nil {
							return err
						}
						executable, err := os.Executable()
						if err != nil {
							return err
						}
						command := exec.CommandContext(ctx, executable, "-test.run=^TestCampaignRecoveryProcessWorker$", "-test.timeout=30s")
						command.Env = append(os.Environ(), "GREGALE_TEST_PROCESS_WORKER=1",
							"GREGALE_TEST_PROCESS_DIRECTORY="+directory, testCampaignJournalEnv+"="+journal)
						command.Stdout, command.Stderr = os.Stderr, os.Stderr
						// Publish only after Start so signal delivery cannot race startup.
						configureTestCampaignWorker(command)
						if err := command.Start(); err != nil {
							return err
						}
						started <- command
						return command.Wait()
					})
			}()
			var command *exec.Cmd
			select {
			case command = <-started:
			case err := <-done:
				t.Fatalf("worker did not start: %v", err)
			case <-time.After(20 * time.Second):
				t.Fatal("worker startup timed out")
			}
			t.Cleanup(func() { _ = command.Process.Kill() })
			waitProcessRecoveryFile(t, filepath.Join(f.directory, "ready"))
			switch mode {
			case "context_interrupt":
				cancel()
			case "sigterm":
				if err := command.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
			case "kill":
				if err := command.Process.Kill(); err != nil {
					t.Fatal(err)
				}
			}
			if mode != "kill" {
				waitProcessRecoveryFile(t, filepath.Join(f.directory, "cleanup-started"))
				select {
				case err := <-done:
					t.Fatalf("scheduler returned before cleanup drained: %v", err)
				default:
				}
				processRecoveryMarker(t, f.directory, "release-cleanup")
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("missing final report accepted")
				}
			case <-time.After(20 * time.Second):
				t.Fatal("scheduler did not drain worker")
			}
			if !f.checkpoint.Halted || f.checkpoint.Batches["b"].Status != "uncertain" {
				t.Fatalf("interruption lost uncertainty: %+v", f.checkpoint)
			}
			if _, ok := f.checkpoint.Batches["c"]; ok {
				t.Fatal("scheduled work after interruption")
			}
			var saved testCampaignCheckpoint
			if err := readTestCampaignJSON(filepath.Join(f.directory, "checkpoint.json"), &saved); err != nil {
				t.Fatal(err)
			}
			targets, _, err := readTestCampaignJournalTargets(f.directory, testCampaignJSONHash(f.plan), saved, f.plan.Batches[1], f.scenarios["checkout"])
			if err != nil {
				t.Fatal(err)
			}
			_, deletes := stub.counts()
			if mode == "kill" {
				if deletes != 0 || len(targets) != 1 {
					t.Fatalf("killed worker lost recoverable identity: targets=%v deletes=%d", targets, deletes)
				}
				client, err := authedClient()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := testCampaignRetryCleanupTargets(context.Background(), client, targets, true); err != nil {
					t.Fatal(err)
				}
			} else {
				waitProcessRecoveryFile(t, filepath.Join(f.directory, "cleanup-finished"))
				if deletes != 1 || len(targets) != 1 {
					t.Fatalf("graceful cleanup not drained: targets=%v deletes=%d", targets, deletes)
				}
			}
			launches := 0
			if err := runTestCampaignBatches(context.Background(), f.plan, f.scenarios, f.directory, &saved, 1,
				func(testCampaignCostBatch) error { return nil },
				func(context.Context, testCampaignCostBatch, string) error { launches++; return nil }); err == nil || launches != 0 {
				t.Fatal("restart repeated uncertain work")
			}
			if _, err := os.Stat(filepath.Join(f.directory, "batch-b.json")); !os.IsNotExist(err) {
				t.Fatal("interruption manufactured final report")
			}
		})
	}
}
