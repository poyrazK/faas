//go:build linux

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type campaignRecoveryRetryConfig struct {
	Kind, Directory, Plan, Handoff, Ready string
	Steps                                 int
	Owner                                 bool
}

func TestCampaignRecoveryRetryProcess(t *testing.T) {
	path := os.Getenv("GREGALE_TEST_RECOVERY_RETRY")
	if path == "" {
		return
	}
	var config campaignRecoveryRetryConfig
	if err := readTestCampaignJSON(path, &config); err != nil {
		t.Fatal(err)
	}
	var plan testCampaignPolicyPlan
	if err := readTestCampaignJSON(config.Plan, &plan); err != nil {
		t.Fatal(err)
	}
	resolver, err := loadTestCampaignBundleResolver(config.Handoff)
	if err != nil {
		t.Fatal(err)
	}
	if config.Owner {
		release, err := acquireTestCampaignHandoffFor(resolver, config.Directory, testCampaignJSONHash(plan), "run", false)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		if resolver != nil {
			if err := registerTestCampaignHandoffWork(resolver, testCampaignJSONHash(plan), config.Directory, resolver.Index.Side, resolver.Index.Revision); err != nil {
				t.Fatal(err)
			}
		}
		runnerRelease, err := acquireTestCampaignRunnerLock(config.Directory, testCampaignJSONHash(plan))
		if err != nil {
			t.Fatal(err)
		}
		defer runnerRelease()
		var lock testCampaignRunnerLock
		if err := readTestCampaignJSON(filepath.Join(config.Directory, "runner.lock"), &lock); err != nil {
			t.Fatal(err)
		}
		// The fixture process supplies a real, terminal identity for the recorded
		// attempt. Separate acceptance cases cover independently running workers.
		dir := testCampaignProcessDirectory(config.Directory, lock)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		record := testCampaignWorkerProcess{SchemaVersion: 1, LockSHA256: testCampaignJSONHash(lock), BatchID: "b", State: "started", Process: &lock.Coordinator}
		if err := persistTestCampaignProcess(filepath.Join(dir, "b.json"), record); err != nil {
			t.Fatal(err)
		}
	} else {
		guardPath := filepath.Join(config.Directory, "runner-recovery.lock")
		if resolver != nil {
			guardPath = resolver.Index.ClaimPath + ".recovery.lock"
		}
		release, err := acquireTestCampaignRecoveryGuard(guardPath)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		var tx testCampaignRecoveryTransaction
		if resolver == nil {
			var result testCampaignLockAssessment
			tx, result, err = prepareTestCampaignRunnerRecovery(config.Directory, plan)
			if err != nil || !result.Recoverable {
				t.Fatalf("prepare runner: %+v %v", result, err)
			}
		} else {
			var result testCampaignClaimAssessment
			tx, result, err = prepareTestCampaignClaimRecovery(resolver)
			if err != nil || !result.Recoverable {
				t.Fatalf("prepare claim: %+v %v", result, err)
			}
		}
		for step := 0; step < config.Steps; step++ {
			done, err := advanceTestCampaignRecovery(tx)
			if err != nil || done {
				t.Fatalf("unexpected recovery step %d: done=%v %v", step, done, err)
			}
		}
	}
	if err := os.WriteFile(config.Ready, []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	select {} // Parent sends SIGKILL at this durable boundary.
}

func startCampaignRecoveryRetryProcess(t *testing.T, root string, config campaignRecoveryRetryConfig) *exec.Cmd {
	t.Helper()
	path := filepath.Join(root, "helper-config.json")
	if err := writeTestCampaignJSON(path, config); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestCampaignRecoveryRetryProcess$", "-test.timeout=60s")
	command.Env = append(os.Environ(), "GREGALE_TEST_RECOVERY_RETRY="+path)
	command.Stdout, command.Stderr = os.Stderr, os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill() })
	waitProcessRecoveryFile(t, config.Ready)
	return command
}

func killCampaignRecoveryRetryProcess(t *testing.T, command *exec.Cmd) {
	t.Helper()
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("recovery process was not killed")
	}
}

func TestCampaignRecoveryRetryAfterProcessDeath(t *testing.T) {
	// adr: 984 — every durable recovery boundary can be retried after SIGKILL without stealing replacement ownership or repeating work.
	for _, kind := range []string{"runner", "claim"} {
		stages := []string{"prepared", "checkpoint", "runner_unlinked"}
		if kind == "claim" {
			stages = append(stages, "claim_unlinked")
		}
		for step, stage := range stages {
			t.Run(kind+"/"+stage, func(t *testing.T) {
				f := newCampaignStatusTestFiles(t)
				directory, planPath, handoff := f.directory, f.planPath, ""
				var resolver *testCampaignBundleResolver
				if kind == "claim" {
					bundlePath := filepath.Join(f.root, "source.json")
					exportCampaignRecoveryAcceptance(t, f.planPath, f.policyPath, f.manifestPath, f.directory, "", bundlePath)
					claims := filepath.Join(f.root, "claims")
					if err := os.Mkdir(claims, 0700); err != nil {
						t.Fatal(err)
					}
					recipient := importCampaignRecoveryAcceptance(t, bundlePath, filepath.Join(f.root, "recipient"), claims, true)
					directory, planPath, handoff = recipient.work, recipient.plan, recipient.index
					var err error
					resolver, err = loadTestCampaignBundleResolver(handoff)
					if err != nil {
						t.Fatal(err)
					}
				}
				cpPath := filepath.Join(directory, "checkpoint.json")
				var cp testCampaignCheckpoint
				if err := readTestCampaignJSON(cpPath, &cp); err != nil {
					t.Fatal(err)
				}
				cp.Halted = false
				cp.Batches["b"] = testCampaignCheckpointBatch{Status: "running"}
				if err := writeTestCampaignJSON(cpPath, cp); err != nil {
					t.Fatal(err)
				}
				beforeReceipt, err := os.ReadFile(filepath.Join(directory, "batch-a.json"))
				if err != nil {
					t.Fatal(err)
				}
				config := campaignRecoveryRetryConfig{Kind: kind, Directory: directory, Plan: planPath, Handoff: handoff, Ready: filepath.Join(f.root, "owner-ready"), Owner: true}
				owner := startCampaignRecoveryRetryProcess(t, f.root, config)
				killCampaignRecoveryRetryProcess(t, owner)
				config.Owner = false
				config.Steps = step
				config.Ready = filepath.Join(f.root, "recovery-ready")
				recovering := startCampaignRecoveryRetryProcess(t, f.root, config)
				guard := filepath.Join(directory, "runner-recovery.lock")
				if resolver != nil {
					guard = resolver.Index.ClaimPath + ".recovery.lock"
				}
				if release, err := acquireTestCampaignRecoveryGuard(guard); err == nil {
					release()
					t.Fatal("live recovery guard was stolen")
				}
				killCampaignRecoveryRetryProcess(t, recovering)
				recover := func() error {
					if resolver != nil {
						result, err := releaseTestCampaignClaim(resolver)
						if err == nil && !result.Released {
							t.Error("claim did not complete")
						}
						return err
					}
					result, err := releaseTestCampaignRunnerLock(directory, f.plan)
					if err == nil && !result.Released {
						t.Error("runner did not complete")
					}
					return err
				}
				if err := recover(); err != nil {
					t.Fatalf("retry after %s failed: %v", stage, err)
				}
				if err := recover(); err != nil {
					t.Fatalf("completed retry failed: %v", err)
				}
				if err := readTestCampaignJSON(cpPath, &cp); err != nil {
					t.Fatal(err)
				}
				if !cp.Halted || cp.Batches["b"].Status != "uncertain" {
					t.Fatal("retry lost halted uncertainty")
				}
				afterReceipt, _ := os.ReadFile(filepath.Join(directory, "batch-a.json"))
				if !bytes.Equal(beforeReceipt, afterReceipt) {
					t.Fatal("recovery rewrote completed evidence")
				}
				if _, err := os.Stat(filepath.Join(directory, "batch-b.json")); !os.IsNotExist(err) {
					t.Fatal("retry manufactured execution evidence")
				}
				// A new owner's lock supersedes any old completion receipt.
				if resolver != nil {
					release, err := acquireTestCampaignHandoffFor(resolver, directory, testCampaignJSONHash(f.plan), "cleanup", false)
					if err != nil {
						t.Fatal(err)
					}
					result, err := releaseTestCampaignClaim(resolver)
					if err != nil || result.Released || result.Recoverable {
						t.Fatalf("replacement claim was stolen: %+v %v", result, err)
					}
					release()
				} else {
					release, err := acquireTestCampaignRunnerLock(directory, testCampaignJSONHash(f.plan))
					if err != nil {
						t.Fatal(err)
					}
					result, err := releaseTestCampaignRunnerLock(directory, f.plan)
					if err != nil || result.Released || result.Recoverable {
						t.Fatalf("replacement runner was stolen: %+v %v", result, err)
					}
					release()
				}
				cp.Halted = false
				if err := writeTestCampaignJSON(cpPath, cp); err != nil {
					t.Fatal(err)
				}
				if err := recover(); err == nil {
					t.Fatal("changed checkpoint accepted an old receipt")
				}
			})
		}
	}
}

func TestCampaignRecoveryGuardProtocol(t *testing.T) {
	// adr: 984 — stable advisory-lock inodes exclude concurrent recoverers and reject unverifiable legacy guards.
	path := filepath.Join(t.TempDir(), "recovery.lock")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if release, err := acquireTestCampaignRecoveryGuard(path); err == nil {
		release()
		t.Fatal("legacy guard was silently replaced")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	release, err := acquireTestCampaignRecoveryGuard(path)
	if err != nil {
		t.Fatal(err)
	}
	first, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := acquireTestCampaignRecoveryGuard(path); err == nil {
		second()
		t.Fatal("concurrent guard acquired")
	}
	release()
	again, err := acquireTestCampaignRecoveryGuard(path)
	if err != nil {
		t.Fatal(err)
	}
	defer again()
	second, err := os.Stat(path)
	if err != nil || !os.SameFile(first, second) {
		t.Fatal("guard inode was replaced on release")
	}
}
