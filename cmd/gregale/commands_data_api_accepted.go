package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
)

type dataAPIAcceptedContracts struct {
	Version              int      `json:"version"`
	CurrentFingerprint   string   `json:"current_fingerprint"`
	AcceptedFingerprints []string `json:"accepted_fingerprints"`
}

func dataAPIAcceptedContractPolicy(before, after string, changes []dataAPIChange) ([]byte, error) {
	if !dataAPIApprovalFingerprint(before) || !dataAPIApprovalFingerprint(after) || before == after {
		return nil, errors.New("acceptance requires distinct valid contract fingerprints")
	}
	for _, change := range changes {
		if change.Breaking {
			return nil, errors.New("older runtime contracts require zero structural breaking changes, including approved breaks")
		}
	}
	content, err := json.MarshalIndent(dataAPIAcceptedContracts{1, after, []string{before}}, "", "  ")
	return append(content, '\n'), err
}

func dataAPIContractOutputAliases(output, input string) bool {
	out, outErr := filepath.Abs(output)
	in, inErr := filepath.Abs(input)
	if outErr != nil || inErr != nil || out == in {
		return true
	}
	outInfo, outErr := os.Stat(output)
	inInfo, inErr := os.Stat(input)
	return outErr == nil && inErr == nil && os.SameFile(outInfo, inInfo)
}

func readDataAPIAcceptedContractsFile(path string) (*dataAPIAcceptedContracts, error) {
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
		return nil, errors.New("accepted contracts exceed the contract output limit")
	}
	var policy dataAPIAcceptedContracts
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&policy) != nil || decoder.Decode(new(any)) != io.EOF || policy.Version != 1 || !dataAPIApprovalFingerprint(policy.CurrentFingerprint) || len(policy.AcceptedFingerprints) == 0 {
		return nil, errors.New("invalid accepted contracts policy")
	}
	seen := map[string]bool{policy.CurrentFingerprint: true}
	for _, hash := range policy.AcceptedFingerprints {
		if !dataAPIApprovalFingerprint(hash) || seen[hash] {
			return nil, errors.New("accepted fingerprints must be unique, valid and distinct from the current contract")
		}
		seen[hash] = true
	}
	return &policy, nil
}
