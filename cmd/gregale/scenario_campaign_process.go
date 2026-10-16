package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type testCampaignProcessIdentity struct {
	PID       int    `json:"pid"`
	BootID    string `json:"boot_id"`
	Namespace string `json:"pid_namespace"`
	Start     string `json:"start_ticks"`
}

type testCampaignRunnerLock struct {
	SchemaVersion int                         `json:"schema_version"`
	PlanSHA256    string                      `json:"plan_sha256"`
	Nonce         string                      `json:"nonce"`
	Coordinator   testCampaignProcessIdentity `json:"coordinator"`
}

type testCampaignWorkerProcess struct {
	SchemaVersion int                          `json:"schema_version"`
	LockSHA256    string                       `json:"lock_sha256"`
	BatchID       string                       `json:"batch_id"`
	State         string                       `json:"state"`
	Process       *testCampaignProcessIdentity `json:"process,omitempty"`
}

type testCampaignProcessStatus struct {
	BatchID string `json:"batch_id,omitempty"`
	PID     int    `json:"pid,omitempty"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
}

type testCampaignLockAssessment struct {
	SchemaVersion int                         `json:"schema_version"`
	Recoverable   bool                        `json:"recoverable"`
	Released      bool                        `json:"released"`
	Coordinator   testCampaignProcessStatus   `json:"coordinator"`
	Workers       []testCampaignProcessStatus `json:"workers"`
	Blockers      []string                    `json:"blockers"`
	LockSHA256    string                      `json:"lock_sha256"`
	Checkpoint    string                      `json:"checkpoint_sha256"`
	Records       string                      `json:"process_records_sha256"`
}

func testCampaignSyncDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func persistTestCampaignProcess(path string, value any) error {
	if err := writeTestCampaignJSON(path, value); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = file.Sync()
	_ = file.Close()
	if err != nil {
		return err
	}
	return testCampaignSyncDirectory(filepath.Dir(path))
}

func acquireTestCampaignRunnerLock(directory, planHash string) (func(), error) {
	identity, _ := readTestCampaignProcessIdentity(os.Getpid())
	identity.PID = os.Getpid()
	// Native run IDs already provide cryptographically random canonical nonces.
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	nonce := hex.EncodeToString(random[:])
	record := testCampaignRunnerLock{SchemaVersion: 1, PlanSHA256: planHash, Nonce: nonce, Coordinator: identity}
	path := filepath.Join(directory, "runner.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	release := func() { _ = os.Remove(path) }
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
		err = testCampaignSyncDirectory(directory)
	}
	if err != nil {
		release()
		return nil, err
	}
	return release, nil
}

func testCampaignProcessDirectory(directory string, lock testCampaignRunnerLock) string {
	return filepath.Join(directory, "runner-process-"+lock.Nonce)
}

// Persist intent before Start. A crash before identity persistence stays unknown,
// preventing a PID-less worker from being silently treated as terminated.
func runTestCampaignTrackedWorker(command *exec.Cmd, directory, batchID string) error {
	var lock testCampaignRunnerLock
	if err := readTestCampaignJSON(filepath.Join(directory, "runner.lock"), &lock); err != nil {
		return err
	}
	if err := validateTestCampaignRunnerLock(lock); err != nil {
		return err
	}
	processDirectory := testCampaignProcessDirectory(directory, lock)
	if err := os.Mkdir(processDirectory, 0o700); err != nil && !os.IsExist(err) {
		return err
	}
	if err := testCampaignPrivateDirectory(processDirectory); err != nil {
		return err
	}
	if err := testCampaignSyncDirectory(directory); err != nil {
		return err
	}
	if batchID == "" || filepath.Base(batchID) != batchID || strings.ContainsAny(batchID, "/\\") || batchID == "." || batchID == ".." {
		return errors.New("invalid process batch ID")
	}
	path := filepath.Join(processDirectory, batchID+".json")
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return errors.New("worker identity already exists or cannot be inspected")
	}
	record := testCampaignWorkerProcess{SchemaVersion: 1, LockSHA256: testCampaignJSONHash(lock), BatchID: batchID, State: "starting"}
	if err := persistTestCampaignProcess(path, record); err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		// A starting record is intentionally conservative even on launch failure.
		return err
	}
	// Missing platform verification must block recovery, not ordinary execution.
	// Persist the PID even when its domain/start identity cannot be established.
	identity, _ := readTestCampaignProcessIdentity(command.Process.Pid)
	identity.PID = command.Process.Pid
	record.Process, record.State = &identity, "started"
	if identityErr := persistTestCampaignProcess(path, record); identityErr != nil {
		// Never return while an untracked child can continue doing work.
		_ = command.Process.Kill()
		_ = command.Wait()
		return fmt.Errorf("worker identity could not be persisted; worker stopped: %w", identityErr)
	}
	return command.Wait()
}

func validateTestCampaignRunnerLock(lock testCampaignRunnerLock) error {
	nonce, err := hex.DecodeString(lock.Nonce)
	if lock.SchemaVersion != 1 || lock.PlanSHA256 == "" || err != nil || len(nonce) != 16 || hex.EncodeToString(nonce) != lock.Nonce || lock.Coordinator.PID <= 0 {
		return errors.New("legacy or invalid runner lock has no verifiable process identities")
	}
	return nil
}

func inspectTestCampaignRunnerLock(directory string, plan testCampaignPolicyPlan, cp testCampaignCheckpoint) (testCampaignLockAssessment, error) {
	result := testCampaignLockAssessment{SchemaVersion: 1, Workers: []testCampaignProcessStatus{}, Blockers: []string{},
		Coordinator: testCampaignProcessStatus{Status: "unverifiable"}, Checkpoint: testCampaignJSONHash(cp)}
	var lock testCampaignRunnerLock
	if err := readTestCampaignJSON(filepath.Join(directory, "runner.lock"), &lock); err != nil {
		result.Blockers = append(result.Blockers, "Runner lock is missing, legacy, or unreadable; automatic recovery is unavailable.")
		return result, nil
	}
	result.LockSHA256 = testCampaignJSONHash(lock)
	if err := validateTestCampaignRunnerLock(lock); err != nil || lock.PlanSHA256 != testCampaignJSONHash(plan) {
		result.Blockers = append(result.Blockers, "Runner lock does not contain identities bound to this plan.")
		return result, nil
	}
	allowed := map[string]bool{}
	for _, batch := range plan.Batches {
		if batch.Side == cp.Side {
			allowed[batch.ID] = true
		}
	}
	for id := range cp.Batches {
		if !allowed[id] {
			return result, errors.New("checkpoint contains unknown batch")
		}
	}
	return inspectTestCampaignRunnerLockBound(directory, testCampaignJSONHash(plan), cp, false)
}

func inspectTestCampaignRunnerLockBound(directory, planHash string, cp testCampaignCheckpoint, allowFrozen bool) (testCampaignLockAssessment, error) {
	result := testCampaignLockAssessment{SchemaVersion: 1, Workers: []testCampaignProcessStatus{}, Blockers: []string{}, Coordinator: testCampaignProcessStatus{Status: "unverifiable"}, Checkpoint: testCampaignJSONHash(cp)}
	var lock testCampaignRunnerLock
	if err := readTestCampaignJSON(filepath.Join(directory, "runner.lock"), &lock); err != nil {
		return result, err
	}
	if err := validateTestCampaignRunnerLock(lock); err != nil || lock.PlanSHA256 != planHash {
		return result, errors.New("runner binding changed")
	}
	result.LockSHA256 = testCampaignJSONHash(lock)
	result.Coordinator.PID = lock.Coordinator.PID
	result.Coordinator.Status, result.Coordinator.Reason = probeTestCampaignProcess(lock.Coordinator)
	if result.Coordinator.Status != "terminated" {
		result.Blockers = append(result.Blockers, "Coordinator termination is not verified.")
	}
	allowed := map[string]bool{}
	for id := range cp.Batches {
		allowed[id] = true
	}
	for _, saved := range cp.Batches {
		if saved.Status != "running" && saved.Status != "uncertain" && saved.Status != "done" {
			return result, errors.New("checkpoint contains unknown batch or state")
		}
	}
	processDirectory := testCampaignProcessDirectory(directory, lock)
	entries, err := os.ReadDir(processDirectory)
	if err != nil && !os.IsNotExist(err) {
		return result, err
	}
	if err == nil {
		if err := testCampaignPrivateDirectory(processDirectory); err != nil {
			return result, err
		}
	}
	if len(entries) > 4096 {
		return result, errors.New("process inventory exceeds 4096 records")
	}
	records := map[string]testCampaignWorkerProcess{}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			return result, errors.New("unexpected process inventory entry")
		}
		var record testCampaignWorkerProcess
		if err := readTestCampaignJSON(filepath.Join(processDirectory, entry.Name()), &record); err != nil {
			return result, err
		}
		_, attempted := cp.Batches[record.BatchID]
		if record.SchemaVersion != 1 || record.LockSHA256 != result.LockSHA256 || !allowed[record.BatchID] || !attempted || entry.Name() != record.BatchID+".json" ||
			(record.State != "starting" && record.State != "started") {
			return result, errors.New("worker process binding changed")
		}
		records[record.BatchID] = record
	}
	result.Records = testCampaignJSONHash(records)
	ids := []string{}
	for id, saved := range cp.Batches {
		_, recorded := records[id]
		if saved.Status == "running" || recorded {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		record, ok := records[id]
		status := testCampaignProcessStatus{BatchID: id, Status: "unverifiable", Reason: "worker identity was not persisted"}
		if ok && record.State == "started" && record.Process != nil {
			status.PID = record.Process.PID
			status.Status, status.Reason = probeTestCampaignProcess(*record.Process)
		}
		if status.Status != "terminated" {
			result.Blockers = append(result.Blockers, "Worker "+id+" termination is not verified.")
		}
		result.Workers = append(result.Workers, status)
	}
	for _, name := range []string{testCampaignRecoverySealName, testCampaignHandoffName} {
		if _, err := os.Lstat(filepath.Join(directory, name)); !allowFrozen && !os.IsNotExist(err) {
			result.Blockers = append(result.Blockers, "Campaign is sealed, transferred, or its ownership cannot be inspected.")
		}
	}
	result.Recoverable = len(result.Blockers) == 0
	return result, nil
}

func releaseTestCampaignRunnerLock(directory string, plan testCampaignPolicyPlan) (testCampaignLockAssessment, error) {
	guardPath := filepath.Join(directory, "runner-recovery.lock")
	guard, err := os.OpenFile(guardPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return testCampaignLockAssessment{}, err
	}
	_ = guard.Close()
	defer func() { _ = os.Remove(guardPath) }()
	var cp testCampaignCheckpoint
	if err := readTestCampaignJSON(filepath.Join(directory, "checkpoint.json"), &cp); err != nil {
		return testCampaignLockAssessment{}, err
	}
	if cp.SchemaVersion != 1 || cp.PlanSHA256 != testCampaignJSONHash(plan) || !testCampaignCheckpointLineageMatches(cp, plan) || cp.Batches == nil {
		return testCampaignLockAssessment{}, errors.New("checkpoint binding changed")
	}
	result, err := inspectTestCampaignRunnerLock(directory, plan, cp)
	if err != nil || !result.Recoverable {
		return result, err
	}
	// Once the coordinator and all tracked workers have terminated, no writer
	// can extend the inventory. Recheck bindings before changing the checkpoint.
	check, err := inspectTestCampaignRunnerLock(directory, plan, cp)
	if err != nil || !check.Recoverable || check.LockSHA256 != result.LockSHA256 || check.Records != result.Records {
		return result, errors.New("process evidence changed during recovery")
	}
	var current testCampaignCheckpoint
	if err := readTestCampaignJSON(filepath.Join(directory, "checkpoint.json"), &current); err != nil || testCampaignJSONHash(current) != result.Checkpoint {
		return result, errors.New("checkpoint changed during recovery")
	}
	cp.Halted = true
	for id, batch := range cp.Batches {
		if batch.Status == "running" {
			batch.Status, batch.Reason = "uncertain", "coordinator terminated; reconcile journal and report evidence before continuing"
			cp.Batches[id] = batch
		}
	}
	// Halt is durable before removing the exclusion lock. Recovery never runs
	// workloads, deletes resources, adopts receipts, or refunds attempted cost.
	if err := persistTestCampaignProcess(filepath.Join(directory, "checkpoint.json"), cp); err != nil {
		return result, err
	}
	var lock testCampaignRunnerLock
	if err := readTestCampaignJSON(filepath.Join(directory, "runner.lock"), &lock); err != nil || testCampaignJSONHash(lock) != result.LockSHA256 {
		return result, errors.New("runner lock changed during recovery")
	}
	if err := os.Remove(filepath.Join(directory, "runner.lock")); err != nil {
		return result, err
	}
	result.Released = true
	return result, testCampaignSyncDirectory(directory)
}

func cmdTestCampaignRecoverLock(args []string) int {
	fs := newFlagSet("test policy recover-lock", flag.ContinueOnError)
	planPath := fs.String("plan", "", "saved campaign plan")
	directory := fs.String("directory", "", "original local campaign directory")
	handoff := fs.String("handoff", "", "active portable import index, when applicable")
	execute := fs.Bool("execute", false, "release a lock only after all recorded processes are verified terminated")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || fs.NArg() != 0 || *planPath == "" || *directory == "" {
		return printErr("Invalid lock recovery options", errors.New("--plan and --directory required; inspection is the default"))
	}
	absolute, err := filepath.Abs(*directory)
	if err != nil {
		return printErr("Invalid campaign directory", err)
	}
	*directory = absolute
	if err := testCampaignPrivateDirectory(*directory); err != nil {
		return printErr("Invalid campaign directory", err)
	}
	resolver, err := loadTestCampaignBundleResolver(*handoff)
	if err != nil {
		return printErr("Invalid handoff", err)
	}
	if err := enforceTestCampaignPlanHandoff(*planPath, resolver); err != nil {
		return printErr("Invalid handoff plan", err)
	}
	if err := checkTestCampaignBundleWork(*directory, resolver, *execute); err != nil {
		return printErr("Invalid campaign ownership", err)
	}
	var plan testCampaignPolicyPlan
	var cp testCampaignCheckpoint
	if err := readTestCampaignJSON(*planPath, &plan); err != nil {
		return printErr("Invalid plan", err)
	}
	if err := readTestCampaignJSON(filepath.Join(*directory, "checkpoint.json"), &cp); err != nil {
		return printErr("Invalid checkpoint", err)
	}
	if cp.SchemaVersion != 1 || cp.PlanSHA256 != testCampaignJSONHash(plan) || cp.Batches == nil || !testCampaignCheckpointLineageMatches(cp, plan) {
		return printErr("Checkpoint binding changed", errors.New("checkpoint must match the plan"))
	}
	if err := validateTestCampaignAssessmentRevision(cp); err != nil {
		return printErr("Invalid checkpoint", err)
	}
	inputs := []string{*planPath, filepath.Join(*directory, "checkpoint.json"), filepath.Join(*directory, "runner.lock")}
	if err := protectTestCampaignStatusOutputs(nil, inputs, []string{*directory}); err != nil {
		return printErr("Invalid recovery output", err)
	}
	result, err := inspectTestCampaignRunnerLock(*directory, plan, cp)
	if *execute && err == nil {
		if err := resolver.requireActive(); err != nil {
			return printErr("Inactive handoff", err)
		}
		release, claimErr := acquireTestCampaignHandoffFor(resolver, *directory, testCampaignJSONHash(plan), "recover-runner", false)
		if claimErr != nil {
			return printErr("Handoff unavailable", claimErr)
		}
		defer release()
		result, err = releaseTestCampaignRunnerLock(*directory, plan)
	}
	if err != nil {
		return printErr("Cannot recover runner lock", err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		return printErr("Could not write process assessment", err)
	}
	if *execute && !result.Released {
		return 2
	}
	return 0
}
