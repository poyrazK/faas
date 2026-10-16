package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type testCampaignClaimLock struct {
	SchemaVersion int                         `json:"schema_version"`
	BundleID      string                      `json:"bundle_id"`
	IndexSHA256   string                      `json:"index_sha256"`
	ClaimNonce    string                      `json:"claim_nonce"`
	Directory     string                      `json:"directory"`
	PlanSHA256    string                      `json:"plan_sha256"`
	Operation     string                      `json:"operation"`
	Owner         testCampaignProcessIdentity `json:"owner"`
}

type testCampaignClaimWorkStatus struct {
	Directory  string                      `json:"directory"`
	PlanSHA256 string                      `json:"plan_sha256"`
	Runner     *testCampaignLockAssessment `json:"runner,omitempty"`
	Checkpoint string                      `json:"checkpoint_sha256"`
	Frozen     bool                        `json:"frozen"`
}

type testCampaignClaimAssessment struct {
	SchemaVersion int                           `json:"schema_version"`
	Recoverable   bool                          `json:"recoverable"`
	Released      bool                          `json:"released"`
	Owner         testCampaignProcessStatus     `json:"owner"`
	Operation     string                        `json:"operation"`
	Work          []testCampaignClaimWorkStatus `json:"work"`
	Blockers      []string                      `json:"blockers"`
	ClaimSHA256   string                        `json:"claim_sha256"`
	LockSHA256    string                        `json:"lock_sha256"`
}

func acquireTestCampaignHandoffFor(resolver *testCampaignBundleResolver, directory, planHash, operation string, allowTransferred bool) (func(), error) {
	if resolver == nil {
		return func() {}, nil
	}
	if err := resolver.requireClaim(allowTransferred); err != nil {
		return nil, err
	}
	identity, _ := readTestCampaignProcessIdentity(os.Getpid())
	identity.PID = os.Getpid()
	record := testCampaignClaimLock{1, resolver.Index.BundleID, testCampaignJSONHash(resolver.Index), resolver.Index.Nonce, directory, planHash, operation, identity}
	path := resolver.Index.ClaimPath + ".lock"
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, errors.New("handoff ownership is busy; inspect a stale lock with test policy recover-claim")
	}
	body, err := json.Marshal(record)
	if err == nil {
		_, err = file.Write(body)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = testCampaignSyncDirectory(filepath.Dir(path))
	}
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	// The claim may have changed while waiting to acquire exclusion.
	if err := resolver.requireClaim(allowTransferred); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return func() { _ = os.Remove(path) }, nil
}

// Inspect process liveness independently of receipt validity. The shared verifier
// binds the checkpoint and worker inventory to the original plan hash; no
// execution evidence is adopted here.
func inspectTestCampaignClaimRunner(directory, planHash string, cp testCampaignCheckpoint) (testCampaignLockAssessment, error) {
	var lock testCampaignRunnerLock
	if err := readTestCampaignJSON(filepath.Join(directory, "runner.lock"), &lock); err != nil {
		return testCampaignLockAssessment{}, err
	}
	if err := validateTestCampaignRunnerLock(lock); err != nil || lock.PlanSHA256 != planHash {
		return testCampaignLockAssessment{}, errors.New("runner lock binding changed")
	}
	return inspectTestCampaignRunnerLockBound(directory, planHash, cp, true)
}

func inspectTestCampaignClaim(resolver *testCampaignBundleResolver) (testCampaignClaimAssessment, error) {
	result := testCampaignClaimAssessment{SchemaVersion: 1, Owner: testCampaignProcessStatus{Status: "unverifiable"}, Work: []testCampaignClaimWorkStatus{}, Blockers: []string{}}
	if resolver == nil {
		return result, errors.New("--handoff is required")
	}
	if err := resolver.requireClaim(true); err != nil {
		return result, err
	}
	var claim testCampaignBundleClaim
	if err := readTestCampaignJSON(resolver.Index.ClaimPath, &claim); err != nil {
		return result, err
	}
	result.ClaimSHA256 = testCampaignJSONHash(claim)
	var lock testCampaignClaimLock
	if err := readTestCampaignJSON(resolver.Index.ClaimPath+".lock", &lock); err != nil {
		result.Blockers = append(result.Blockers, "Claim lock is missing, legacy or unreadable.")
		return result, nil
	}
	result.LockSHA256 = testCampaignJSONHash(lock)
	if lock.SchemaVersion != 1 || lock.BundleID != resolver.Index.BundleID || lock.IndexSHA256 != testCampaignJSONHash(resolver.Index) || lock.ClaimNonce != resolver.Index.Nonce || !filepath.IsAbs(lock.Directory) || lock.PlanSHA256 == "" {
		result.Blockers = append(result.Blockers, "Claim lock lacks a matching operation and target binding.")
		return result, nil
	}
	switch lock.Operation {
	case "run", "cleanup", "reconcile", "export", "recover-runner":
	default:
		return result, errors.New("unknown claim operation")
	}
	result.Operation = lock.Operation
	result.Owner.PID = lock.Owner.PID
	result.Owner.Status, result.Owner.Reason = probeTestCampaignProcess(lock.Owner)
	if result.Owner.Status != "terminated" {
		result.Blockers = append(result.Blockers, "Claim owner termination is not verified.")
	}
	targets := map[string]string{}
	for hash, directory := range claim.Work {
		if previous, ok := targets[directory]; ok && previous != hash {
			return result, errors.New("work directory has conflicting plan bindings")
		}
		targets[directory] = hash
	}
	if hash, ok := targets[lock.Directory]; ok && hash != lock.PlanSHA256 {
		return result, errors.New("operation target conflicts with registered work")
	}
	targets[lock.Directory] = lock.PlanSHA256
	if len(targets) > 256 {
		return result, errors.New("claim recovery exceeds 256 work directories")
	}
	directories := []string{}
	for directory := range targets {
		directories = append(directories, directory)
	}
	sort.Strings(directories)
	for _, directory := range directories {
		hash := targets[directory]
		if !filepath.IsAbs(directory) || testCampaignBundleImmutableDirectory(directory, resolver) {
			return result, errors.New("invalid mutable work directory")
		}
		if err := testCampaignPrivateDirectory(directory); err != nil {
			return result, err
		}
		var cp testCampaignCheckpoint
		if err := readTestCampaignJSON(filepath.Join(directory, "checkpoint.json"), &cp); err != nil {
			return result, err
		}
		if cp.SchemaVersion != 1 || cp.PlanSHA256 != hash || cp.Batches == nil {
			return result, errors.New("work checkpoint binding changed")
		}
		if err := validateTestCampaignAssessmentRevision(cp); err != nil {
			return result, err
		}
		row := testCampaignClaimWorkStatus{Directory: directory, PlanSHA256: hash, Checkpoint: testCampaignJSONHash(cp)}
		// A frozen or sealed checkpoint is immutable. Releasing exclusion cannot
		// revoke its seal, restore authority, or rewrite its checkpoint digest.
		for _, name := range []string{testCampaignHandoffName, testCampaignRecoverySealName} {
			if _, err := os.Lstat(filepath.Join(directory, name)); err == nil {
				row.Frozen = true
			} else if !os.IsNotExist(err) {
				return result, err
			}
		}
		if _, err := os.Lstat(filepath.Join(directory, "runner.lock")); err == nil {
			runner, err := inspectTestCampaignClaimRunner(directory, hash, cp)
			if err != nil {
				return result, err
			}
			row.Runner = &runner
			if !runner.Recoverable {
				result.Blockers = append(result.Blockers, "Runner or worker termination is not verified in "+directory)
			}
		} else if !os.IsNotExist(err) {
			return result, err
		} else {
			for _, batch := range cp.Batches {
				if batch.Status == "running" && !row.Frozen {
					result.Blockers = append(result.Blockers, "Running checkpoint has no runner identity in "+directory)
				}
			}
		}
		result.Work = append(result.Work, row)
	}
	result.Recoverable = len(result.Blockers) == 0
	return result, nil
}

// Reaping a zombie changes the diagnostic reason, not the terminal identity
// proof. Preserve every binding and status while excluding only descriptions
// from the read/recheck fingerprint.
func testCampaignClaimRecoveryStamp(result testCampaignClaimAssessment) string {
	result.Owner.Reason = ""
	result.Work = append([]testCampaignClaimWorkStatus(nil), result.Work...)
	for i := range result.Work {
		if result.Work[i].Runner == nil {
			continue
		}
		runner := *result.Work[i].Runner
		runner.Coordinator.Reason = ""
		runner.Workers = append([]testCampaignProcessStatus(nil), runner.Workers...)
		for j := range runner.Workers {
			runner.Workers[j].Reason = ""
		}
		result.Work[i].Runner = &runner
	}
	return testCampaignJSONHash(result)
}

func prepareTestCampaignClaimRecovery(resolver *testCampaignBundleResolver) (testCampaignRecoveryTransaction, testCampaignClaimAssessment, error) {
	result, err := inspectTestCampaignClaim(resolver)
	if err != nil || !result.Recoverable {
		return testCampaignRecoveryTransaction{}, result, err
	}
	fresh, err := inspectTestCampaignClaim(resolver)
	if err != nil || !fresh.Recoverable || testCampaignClaimRecoveryStamp(fresh) != testCampaignClaimRecoveryStamp(result) {
		return testCampaignRecoveryTransaction{}, result, errors.New("claim or process evidence changed during recovery")
	}
	tx := testCampaignRecoveryTransaction{SchemaVersion: 1, Kind: "claim", Scope: resolver.Index.ClaimPath, Binding: testCampaignJSONHash(resolver.Index), Claim: &result}
	for _, row := range result.Work {
		var cp testCampaignCheckpoint
		if err := readTestCampaignJSON(filepath.Join(row.Directory, "checkpoint.json"), &cp); err != nil || testCampaignJSONHash(cp) != row.Checkpoint {
			return tx, result, errors.New("checkpoint changed during recovery")
		}
		after := cp
		if row.Runner != nil && !row.Frozen {
			after = haltedTestCampaignCheckpoint(cp, "handoff owner terminated; reconcile before continuing")
		}
		tx.Checkpoints = append(tx.Checkpoints, testCampaignRecoveryCheckpoint{row.Directory, row.Checkpoint, after})
		if row.Runner != nil {
			tx.Removals = append(tx.Removals, testCampaignRecoveryRemoval{filepath.Join(row.Directory, "runner.lock"), row.Runner.LockSHA256})
		}
	}
	tx.Removals = append(tx.Removals, testCampaignRecoveryRemoval{resolver.Index.ClaimPath + ".lock", result.LockSHA256})
	if err := persistTestCampaignRecoveryTransaction(resolver.Index.ClaimPath+".recovery.json", tx); err != nil {
		return tx, result, err
	}
	return tx, result, nil
}

func releaseTestCampaignClaim(resolver *testCampaignBundleResolver) (testCampaignClaimAssessment, error) {
	return releaseTestCampaignClaimChecked(resolver, nil)
}

func releaseTestCampaignClaimChecked(resolver *testCampaignBundleResolver, verify func() error) (testCampaignClaimAssessment, error) {
	if resolver == nil {
		return testCampaignClaimAssessment{}, errors.New("--handoff is required")
	}
	// An inspection-only or mismatched import must not create a persistent
	// guard at an empty or otherwise unauthorized claim path.
	if err := resolver.requireClaim(true); err != nil {
		return testCampaignClaimAssessment{}, err
	}
	release, err := acquireTestCampaignRecoveryGuard(resolver.Index.ClaimPath + ".recovery.lock")
	if err != nil {
		return testCampaignClaimAssessment{}, err
	}
	defer release()
	if verify != nil {
		if err := verify(); err != nil {
			return testCampaignClaimAssessment{}, err
		}
	}
	if _, err := os.Lstat(resolver.Index.ClaimPath + ".lock"); os.IsNotExist(err) {
		if err := resolver.requireClaim(true); err != nil {
			return testCampaignClaimAssessment{}, err
		}
		tx, err := readFinishedTestCampaignRecovery(resolver.Index.ClaimPath+".recovery.json", "claim", resolver.Index.ClaimPath, testCampaignJSONHash(resolver.Index))
		if err != nil {
			return testCampaignClaimAssessment{}, err
		}
		if tx.Claim == nil || !tx.Claim.Recoverable {
			return testCampaignClaimAssessment{}, errors.New("invalid claim recovery receipt")
		}
		var claim testCampaignBundleClaim
		if err := readTestCampaignJSON(resolver.Index.ClaimPath, &claim); err != nil || testCampaignJSONHash(claim) != tx.Claim.ClaimSHA256 {
			return testCampaignClaimAssessment{}, errors.New("claim changed since recovery")
		}
		result := *tx.Claim
		if err := finishTestCampaignRecovery(tx); err != nil {
			return result, err
		}
		result.Released = true
		return result, nil
	}
	tx, result, err := prepareTestCampaignClaimRecovery(resolver)
	if err != nil || !result.Recoverable {
		return result, err
	}
	// Exclusion still belongs to the verified dead owner until the final step.
	var claim testCampaignBundleClaim
	if err := readTestCampaignJSON(resolver.Index.ClaimPath, &claim); err != nil || testCampaignJSONHash(claim) != result.ClaimSHA256 {
		return result, errors.New("claim changed during recovery")
	}
	if err := finishTestCampaignRecovery(tx); err != nil {
		return result, err
	}
	result.Released = true
	return result, nil
}

func cmdTestCampaignRecoverClaim(args []string) int {
	fs := newFlagSet("test policy recover-claim", flag.ContinueOnError)
	handoff := fs.String("handoff", "", "activated portable import index")
	execute := fs.Bool("execute", false, "release claim and runner exclusion only after verified termination")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || fs.NArg() != 0 || *handoff == "" {
		return printErr("Invalid claim recovery options", errors.New("--handoff required; inspection is the default"))
	}
	resolver, err := loadTestCampaignBundleResolver(*handoff)
	if err != nil {
		return printErr("Invalid handoff", err)
	}
	inputs := []string{resolver.IndexPath, resolver.Index.ClaimPath, resolver.Index.ClaimPath + ".lock"}
	if err := protectTestCampaignStatusOutputs(nil, inputs, []string{resolver.Index.Root}); err != nil {
		return printErr("Invalid recovery output", err)
	}
	var result testCampaignClaimAssessment
	if *execute {
		result, err = releaseTestCampaignClaim(resolver)
	} else {
		result, err = inspectTestCampaignClaim(resolver)
	}
	if err != nil {
		return printErr("Cannot recover handoff claim", fmt.Errorf("inspect retained claim and work evidence: %w", err))
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		return printErr("Could not write claim assessment", err)
	}
	if *execute && !result.Released {
		return 2
	}
	return 0
}
