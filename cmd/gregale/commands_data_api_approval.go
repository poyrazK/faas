package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// An approval is scoped to exact contract bytes and exact breaking changes.
// Keeping the original baseline lets CI continue to report the approved break.
type dataAPIApproval struct {
	Version             int                     `json:"version"`
	BaselineFingerprint string                  `json:"baseline_fingerprint"`
	CurrentFingerprint  string                  `json:"current_fingerprint"`
	Changes             []dataAPIApprovedChange `json:"changes"`
}

type dataAPIApprovedChange struct {
	Path      string `json:"path"`
	Reason    string `json:"reason"`
	Rationale string `json:"rationale"`
}

func readDataAPIApprovalFile(path string) (*dataAPIApproval, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, api.AppTaskDefaultMaxOutputBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) > api.AppTaskDefaultMaxOutputBytes {
		return nil, errors.New("compatibility approval exceeds contract output limit")
	}
	var approval dataAPIApproval
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&approval) != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("invalid compatibility approval JSON")
	}
	if approval.Version != 1 || !dataAPIApprovalFingerprint(approval.BaselineFingerprint) || !dataAPIApprovalFingerprint(approval.CurrentFingerprint) || len(approval.Changes) == 0 {
		return nil, errors.New("approval requires version 1, two SHA-256 fingerprints and named changes")
	}
	seen := map[[2]string]bool{}
	for _, change := range approval.Changes {
		key := [2]string{change.Path, change.Reason}
		if strings.TrimSpace(change.Path) == "" || strings.TrimSpace(change.Reason) == "" || strings.TrimSpace(change.Rationale) == "" || seen[key] {
			return nil, errors.New("approval changes require unique path/reason pairs and a rationale")
		}
		seen[key] = true
	}
	return &approval, nil
}

func dataAPIApprovalFingerprint(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && value == strings.ToLower(value)
}

func applyDataAPIApproval(approval *dataAPIApproval, before, after string, changes []dataAPIChange) error {
	if approval == nil {
		return nil
	}
	if approval.BaselineFingerprint != before || approval.CurrentFingerprint != after {
		return errors.New("approval fingerprints do not match the compared contracts; review changes again")
	}
	indices := map[[2]string]int{}
	for i, change := range changes {
		if change.Breaking {
			indices[[2]string{change.Path, change.Reason}] = i
		}
	}
	for _, approved := range approval.Changes {
		key := [2]string{approved.Path, approved.Reason}
		i, exists := indices[key]
		if !exists {
			return errors.New("approval names a change absent from the breaking report; review changes again")
		}
		changes[i].Approved = true
		delete(indices, key)
	}
	return nil
}
