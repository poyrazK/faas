//go:build linux

package main

import (
	"bytes"
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

type campaignClaimCrashConfig struct{ Handoff, Plan, Policy, Manifest, Directory, Output, Mode string }

func TestCampaignRecoveryClaimOwnerProcess(t *testing.T) {
	path := os.Getenv("GREGALE_TEST_CLAIM_CONFIG")
	if path == "" {
		return
	}
	var config campaignClaimCrashConfig
	if err := readTestCampaignJSON(path, &config); err != nil {
		t.Fatal(err)
	}
	args := []string{"--plan", config.Plan, "--policy", config.Policy, "--manifest", config.Manifest, "--directory", config.Directory, "--handoff", config.Handoff}
	switch config.Mode {
	case "cleanup":
		if code := cmdTestCampaignControl("cleanup", append(args, "--batch", "b", "--journal", "--execute")); code != 0 {
			t.Fatalf("cleanup: %d", code)
		}
	case "export":
		if code := cmdTestCampaignBundleExport(append(args, "--output", config.Output)); code != 0 {
			t.Fatalf("export: %d", code)
		}
	case "run":
		resolver, err := loadTestCampaignBundleResolver(config.Handoff)
		if err != nil {
			t.Fatal(err)
		}
		var plan testCampaignPolicyPlan
		var cp testCampaignCheckpoint
		if err := readTestCampaignJSON(config.Plan, &plan); err != nil {
			t.Fatal(err)
		}
		if err := readTestCampaignJSON(filepath.Join(config.Directory, "checkpoint.json"), &cp); err != nil {
			t.Fatal(err)
		}
		release, err := acquireTestCampaignHandoffFor(resolver, config.Directory, testCampaignJSONHash(plan), "run", false)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		if err := registerTestCampaignHandoffWork(resolver, testCampaignJSONHash(plan), config.Directory, cp.Side, cp.Revision); err != nil {
			t.Fatal(err)
		}
		releaseRunner, err := acquireTestCampaignRunnerLock(config.Directory, testCampaignJSONHash(plan))
		if err != nil {
			t.Fatal(err)
		}
		defer releaseRunner()
		batch := plan.Batches[1]
		cp.Batches[batch.ID] = testCampaignCheckpointBatch{Status: "running"}
		if err := persistTestCampaignProcess(filepath.Join(config.Directory, "checkpoint.json"), cp); err != nil {
			t.Fatal(err)
		}
		journal, err := prepareTestCampaignJournal(config.Directory, batch)
		if err != nil {
			t.Fatal(err)
		}
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		command := exec.CommandContext(context.Background(), executable, "-test.run=^TestCampaignRecoveryProcessWorker$", "-test.timeout=60s")
		command.Env = append(os.Environ(), "GREGALE_TEST_PROCESS_WORKER=1", "GREGALE_TEST_PROCESS_DIRECTORY="+config.Directory, testCampaignJournalEnv+"="+journal)
		command.Stdout, command.Stderr = os.Stderr, os.Stderr
		configureTestCampaignWorker(command)
		if err := runTestCampaignTrackedWorker(command, config.Directory, batch.ID); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("unknown helper mode")
	}
}

func TestCampaignRecoveryClaimCrash(t *testing.T) {
	// adr: 984 — imported ownership cannot be stolen from live workers, and re-export recovery cannot restore transferred authority.
	for _, mode := range []string{"run", "cleanup", "export"} {
		t.Run(mode, func(t *testing.T) {
			stub := newCampaignRecoveryAcceptanceAPI(t)
			f := interruptedCampaignRecoveryAcceptanceFixture(t, stub, false)
			output := filepath.Join(f.root, "source-bundle.json")
			exportCampaignRecoveryAcceptance(t, f.planPath, f.policyPath, f.manifestPath, f.directory, "", output)
			claims := filepath.Join(f.root, "claims")
			if err := os.Mkdir(claims, 0700); err != nil {
				t.Fatal(err)
			}
			recipient := importCampaignRecoveryAcceptance(t, output, filepath.Join(f.root, "recipient"), claims, true)
			resolver, err := loadTestCampaignBundleResolver(recipient.index)
			if err != nil {
				t.Fatal(err)
			}
			var cp testCampaignCheckpoint
			cpPath := filepath.Join(recipient.work, "checkpoint.json")
			if err := readTestCampaignJSON(cpPath, &cp); err != nil {
				t.Fatal(err)
			}
			if mode == "run" {
				if err := os.RemoveAll(testCampaignJournalDirectory(recipient.work, "b")); err != nil {
					t.Fatal(err)
				}
				delete(cp.Batches, "b")
				cp.Halted = false
				if err := writeTestCampaignJSON(cpPath, cp); err != nil {
					t.Fatal(err)
				}
				if err := writeTestCampaignJSON(filepath.Join(recipient.work, "process-scenario.json"), f.scenarios["checkout"]); err != nil {
					t.Fatal(err)
				}
			}
			started, proceed := make(chan struct{}), make(chan struct{})
			if mode == "cleanup" {
				stub.mu.Lock()
				stub.deleteStarted, stub.deleteContinue = started, proceed
				stub.mu.Unlock()
				defer close(proceed)
			}
			config := campaignClaimCrashConfig{recipient.index, recipient.plan, recipient.policy, f.manifestPath, recipient.work, filepath.Join(f.root, "next.json"), mode}
			configPath := filepath.Join(f.root, "process-config.json")
			if err := writeTestCampaignJSON(configPath, config); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			command := exec.Command(executable, "-test.run=^TestCampaignRecoveryClaimOwnerProcess$", "-test.timeout=60s")
			command.Env = append(os.Environ(), "GREGALE_TEST_CLAIM_CONFIG="+configPath)
			command.Stdout, command.Stderr = os.Stderr, os.Stderr
			if mode == "export" {
				reader, writer, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				defer writer.Close()
				size, _, errno := syscall.Syscall(syscall.SYS_FCNTL, writer.Fd(), 1032, 0) // F_GETPIPE_SZ
				if errno != 0 {
					t.Fatal(errno)
				}
				if _, err := writer.Write(make([]byte, int(size))); err != nil {
					t.Fatal(err)
				}
				command.Stdout = writer // Block final summary after publication, before deferred lock release.
			}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = command.Process.Kill() })
			switch mode {
			case "run":
				waitProcessRecoveryFile(t, filepath.Join(recipient.work, "ready"))
			case "cleanup":
				select {
				case <-started:
				case <-time.After(20 * time.Second):
					t.Fatal("cleanup did not reach deletion")
				}
			case "export":
				waitProcessRecoveryFile(t, config.Output)
			}
			args := []string{"--handoff", recipient.index}
			if code := cmdTestCampaignRecoverClaim(append(append([]string{}, args...), "--execute")); code != 2 {
				t.Fatalf("live claim was not blocked: %d", code)
			}
			var workerRecord testCampaignWorkerProcess
			if mode == "run" {
				var lock testCampaignRunnerLock
				if err := readTestCampaignJSON(filepath.Join(recipient.work, "runner.lock"), &lock); err != nil {
					t.Fatal(err)
				}
				recordPath := filepath.Join(testCampaignProcessDirectory(recipient.work, lock), "b.json")
				deadline := time.Now().Add(10 * time.Second)
				for {
					if err := readTestCampaignJSON(recordPath, &workerRecord); err == nil && workerRecord.Process != nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("worker identity missing")
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			if err := command.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			if err := command.Wait(); err == nil {
				t.Fatal("owner was not killed")
			}
			if mode == "run" {
				if code := cmdTestCampaignRecoverClaim(append(append([]string{}, args...), "--execute")); code != 2 {
					t.Fatal("live orphan worker did not block claim release")
				}
				worker, err := os.FindProcess(workerRecord.Process.PID)
				if err != nil {
					t.Fatal(err)
				}
				if err := worker.Kill(); err != nil {
					t.Fatal(err)
				}
				defer worker.Release()
				deadline := time.Now().Add(10 * time.Second)
				for {
					state, _ := probeTestCampaignProcess(*workerRecord.Process)
					if state == "terminated" {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("worker termination not observed")
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			if mode == "cleanup" {
				proceed <- struct{}{}
			} // Complete the server-side delete after the caller died.
			before, err := os.ReadFile(cpPath)
			if err != nil {
				t.Fatal(err)
			}
			claimBefore, err := os.ReadFile(resolver.Index.ClaimPath)
			if err != nil {
				t.Fatal(err)
			}
			if code := cmdTestCampaignRecoverClaim(args); code != 0 {
				t.Fatalf("inspect failed: %d", code)
			}
			if _, err := os.Stat(resolver.Index.ClaimPath + ".lock"); err != nil {
				t.Fatal("inspection removed claim lock")
			}
			if code := cmdTestCampaignRecoverClaim(append(args, "--execute")); code != 0 {
				t.Fatalf("dead owner could not recover: %d", code)
			}
			claimAfter, _ := os.ReadFile(resolver.Index.ClaimPath)
			if !bytes.Equal(claimBefore, claimAfter) {
				t.Fatal("recovery changed claim authority")
			}
			if _, err := os.Stat(filepath.Join(recipient.work, "runner.lock")); !os.IsNotExist(err) {
				t.Fatal("runner exclusion remains")
			}
			if _, err := os.Stat(resolver.Index.ClaimPath + ".lock"); !os.IsNotExist(err) {
				t.Fatal("claim exclusion remains")
			}
			bundle, err := readTestCampaignBundle(output)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := importTestCampaignBundle(bundle, filepath.Join(f.root, "duplicate"), true, claims); err == nil {
				t.Fatal("recovery allowed duplicate activation")
			}
			if mode == "export" {
				after, _ := os.ReadFile(cpPath)
				if !bytes.Equal(before, after) {
					t.Fatal("frozen checkpoint changed")
				}
				if err := resolver.requireActive(); err == nil {
					t.Fatal("transferred owner regained execution")
				}
				retry := filepath.Join(f.root, "republished.json")
				exportCampaignRecoveryAcceptance(t, recipient.plan, recipient.policy, f.manifestPath, recipient.work, recipient.index, retry)
				original, _ := os.ReadFile(config.Output)
				again, _ := os.ReadFile(retry)
				if !bytes.Equal(original, again) {
					t.Fatal("re-export changed frozen bundle")
				}
			} else {
				if err := readTestCampaignJSON(cpPath, &cp); err != nil {
					t.Fatal(err)
				}
				if !cp.Halted || cp.Batches["b"].Status != "uncertain" {
					t.Fatal("recovery lost uncertain work")
				}
				cleanup := append(recipient.args(f.manifestPath), "--batch", "b", "--journal", "--execute")
				if code := cmdTestCampaignControl("cleanup", cleanup); code != 0 {
					t.Fatalf("cleanup could not resume: %d", code)
				}
				_, deletes := stub.counts()
				if deletes != 1 {
					t.Fatalf("cleanup duplicated or omitted deletion: %d", deletes)
				}
			}
			if _, err := os.Stat(filepath.Join(recipient.work, "batch-b.json")); !os.IsNotExist(err) {
				t.Fatal("recovery manufactured final report")
			}
		})
	}
}

func TestCampaignRecoveryClaimGuards(t *testing.T) {
	// adr: 984 — every registered runner participates in exclusion; stale, legacy and foreign identities never authorize release.
	f, bundle, _ := exportCampaignBundleTestFixture(t, false)
	claims := filepath.Join(f.root, "claims")
	if err := os.Mkdir(claims, 0700); err != nil {
		t.Fatal(err)
	}
	index, err := importTestCampaignBundle(bundle, filepath.Join(f.root, "recipient"), true, claims)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := loadTestCampaignBundleResolver(filepath.Join(index.Root, "bundle-index.json"))
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(index.Root, "work")
	release, err := acquireTestCampaignHandoffFor(resolver, directory, bundle.PlanSHA256, "cleanup", false)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	path := resolver.Index.ClaimPath + ".lock"
	var lock testCampaignClaimLock
	if err := readTestCampaignJSON(path, &lock); err != nil {
		t.Fatal(err)
	}
	start, err := strconv.ParseUint(lock.Owner.Start, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	dead := lock
	dead.Owner.Start = strconv.FormatUint(start+1, 10)
	for _, mode := range []string{"live", "foreign", "legacy", "nonce", "unknown_operation", "starting_worker", "other_runner"} {
		t.Run(mode, func(t *testing.T) {
			record := dead
			switch mode {
			case "live":
				record = lock
			case "foreign":
				record.Owner.BootID = "another-boot"
			case "nonce":
				record.ClaimNonce = "another-claim"
			case "unknown_operation":
				record.Operation = "unrecognized"
			}
			if err := writeTestCampaignJSON(path, record); err != nil {
				t.Fatal(err)
			}
			if mode == "legacy" {
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "starting_worker" {
				runnerRelease, err := acquireTestCampaignRunnerLock(directory, bundle.PlanSHA256)
				if err != nil {
					t.Fatal(err)
				}
				defer runnerRelease()
				var runner testCampaignRunnerLock
				runnerPath := filepath.Join(directory, "runner.lock")
				if err := readTestCampaignJSON(runnerPath, &runner); err != nil {
					t.Fatal(err)
				}
				runner.Coordinator = dead.Owner
				if err := writeTestCampaignJSON(runnerPath, runner); err != nil {
					t.Fatal(err)
				}
				cpPath := filepath.Join(directory, "checkpoint.json")
				before, err := os.ReadFile(cpPath)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := os.WriteFile(cpPath, before, 0600); err != nil {
						t.Error(err)
					}
				}()
				var cp testCampaignCheckpoint
				if err := readTestCampaignJSON(cpPath, &cp); err != nil {
					t.Fatal(err)
				}
				cp.Batches["b"] = testCampaignCheckpointBatch{Status: "running"}
				if err := writeTestCampaignJSON(cpPath, cp); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "other_runner" {
				other := filepath.Join(index.Root, "other-work")
				if err := os.Mkdir(other, 0700); err != nil {
					t.Fatal(err)
				}
				otherHash := strings.Repeat("f", 64)
				var cp testCampaignCheckpoint
				if err := readTestCampaignJSON(filepath.Join(directory, "checkpoint.json"), &cp); err != nil {
					t.Fatal(err)
				}
				cp.PlanSHA256 = otherHash
				if err := writeTestCampaignJSON(filepath.Join(other, "checkpoint.json"), cp); err != nil {
					t.Fatal(err)
				}
				otherRelease, err := acquireTestCampaignRunnerLock(other, otherHash)
				if err != nil {
					t.Fatal(err)
				}
				defer otherRelease()
				raw, err := os.ReadFile(resolver.Index.ClaimPath)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := os.WriteFile(resolver.Index.ClaimPath, raw, 0600); err != nil {
						t.Error(err)
					}
				}()
				var claim testCampaignBundleClaim
				if err := readTestCampaignJSON(resolver.Index.ClaimPath, &claim); err != nil {
					t.Fatal(err)
				}
				if claim.Work == nil {
					claim.Work = map[string]string{}
				}
				claim.Work[otherHash] = other
				if err := writeTestCampaignJSON(resolver.Index.ClaimPath, claim); err != nil {
					t.Fatal(err)
				}
			}
			result, err := releaseTestCampaignClaim(resolver)
			if mode == "unknown_operation" {
				if err == nil {
					t.Fatal("unknown operation accepted")
				}
			} else if err != nil || result.Recoverable || result.Released {
				t.Fatalf("unsafe claim recovery: %+v %v", result, err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal("blocked recovery removed exclusion")
			}
		})
	}
}
