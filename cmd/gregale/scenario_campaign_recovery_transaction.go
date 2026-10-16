package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type testCampaignRecoveryCheckpoint struct {
	Directory string                 `json:"directory"`
	Before    string                 `json:"before_sha256"`
	After     testCampaignCheckpoint `json:"after"`
}
type testCampaignRecoveryRemoval struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type testCampaignRecoveryTransaction struct {
	SchemaVersion int                              `json:"schema_version"`
	Kind          string                           `json:"kind"`
	Scope         string                           `json:"scope"`
	Binding       string                           `json:"binding"`
	Runner        *testCampaignLockAssessment      `json:"runner,omitempty"`
	Claim         *testCampaignClaimAssessment     `json:"claim,omitempty"`
	Checkpoints   []testCampaignRecoveryCheckpoint `json:"checkpoints"`
	Removals      []testCampaignRecoveryRemoval    `json:"removals"`
}

func haltedTestCampaignCheckpoint(cp testCampaignCheckpoint, reason string) testCampaignCheckpoint {
	cp.Halted = true
	batches := make(map[string]testCampaignCheckpointBatch, len(cp.Batches))
	for id, batch := range cp.Batches {
		if batch.Status == "running" {
			batch.Status = "uncertain"
			batch.Reason = reason
		}
		batches[id] = batch
	}
	cp.Batches = batches
	return cp
}

// Each call performs one durable step. This is also the process-boundary seam
// used by acceptance tests; there are no environment-controlled crash hooks.
func advanceTestCampaignRecovery(tx testCampaignRecoveryTransaction) (bool, error) {
	if tx.SchemaVersion != 1 || len(tx.Removals) == 0 || len(tx.Checkpoints) == 0 {
		return false, errors.New("invalid recovery receipt")
	}
	pending := -1
	for i, row := range tx.Checkpoints {
		var cp testCampaignCheckpoint
		if err := readTestCampaignJSON(filepath.Join(row.Directory, "checkpoint.json"), &cp); err != nil {
			return false, err
		}
		hash := testCampaignJSONHash(cp)
		if hash != testCampaignJSONHash(row.After) {
			if hash != row.Before {
				return false, errors.New("checkpoint changed since recovery was prepared")
			}
			if pending < 0 {
				pending = i
			}
		}
	}
	firstPresent := -1
	seenPresent := false
	for i, lock := range tx.Removals {
		var raw json.RawMessage
		err := readTestCampaignJSON(lock.Path, &raw)
		if os.IsNotExist(err) {
			if pending >= 0 || seenPresent {
				return false, errors.New("lock removal order differs from recovery receipt")
			}
			continue
		}
		if err != nil {
			return false, err
		}
		if testCampaignJSONHash(raw) != lock.SHA256 {
			return false, errors.New("a replacement lock blocks recovery retry")
		}
		if firstPresent < 0 {
			firstPresent = i
		}
		seenPresent = true
	}
	if pending >= 0 {
		row := tx.Checkpoints[pending]
		return false, persistTestCampaignProcess(filepath.Join(row.Directory, "checkpoint.json"), row.After)
	}
	// A prior process may have died after rename but before fsync. Matching
	// contents alone are not a durability barrier: sync every checkpoint before
	// unlinking any exclusion lock, including on a resumed step.
	for _, row := range tx.Checkpoints {
		path := filepath.Join(row.Directory, "checkpoint.json")
		file, err := os.Open(path)
		if err != nil {
			return false, err
		}
		err = file.Sync()
		_ = file.Close()
		if err != nil {
			return false, err
		}
		if err := testCampaignSyncDirectory(row.Directory); err != nil {
			return false, err
		}
	}
	if firstPresent >= 0 {
		path := tx.Removals[firstPresent].Path
		if err := os.Remove(path); err != nil {
			return false, err
		}
		return false, testCampaignSyncDirectory(filepath.Dir(path))
	}
	// A retry after the final unlink must also persist that deletion before
	// reporting completion. Never delete or rewrite anything in this path.
	for _, lock := range tx.Removals {
		if err := testCampaignSyncDirectory(filepath.Dir(lock.Path)); err != nil {
			return false, err
		}
	}
	return true, nil
}

func finishTestCampaignRecovery(tx testCampaignRecoveryTransaction) error {
	for {
		done, err := advanceTestCampaignRecovery(tx)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}

func readFinishedTestCampaignRecovery(path, kind, scope, binding string) (testCampaignRecoveryTransaction, error) {
	var tx testCampaignRecoveryTransaction
	if err := readTestCampaignJSON(path, &tx); err != nil {
		return tx, err
	}
	if tx.SchemaVersion != 1 || tx.Kind != kind || tx.Scope != scope || tx.Binding != binding || len(tx.Removals) == 0 || len(tx.Checkpoints) == 0 {
		return tx, errors.New("recovery receipt binding changed")
	}
	// This path is only for a missing final lock. No pending step is authorized
	// from an old receipt: all checkpoint and removal postconditions must hold.
	for _, row := range tx.Checkpoints {
		var cp testCampaignCheckpoint
		if err := readTestCampaignJSON(filepath.Join(row.Directory, "checkpoint.json"), &cp); err != nil || testCampaignJSONHash(cp) != testCampaignJSONHash(row.After) {
			return tx, errors.New("recovery receipt no longer matches checkpoint")
		}
	}
	for _, lock := range tx.Removals {
		if _, err := os.Lstat(lock.Path); !os.IsNotExist(err) {
			return tx, errors.New("new or unreadable lock blocks completed recovery retry")
		}
	}
	return tx, nil
}
