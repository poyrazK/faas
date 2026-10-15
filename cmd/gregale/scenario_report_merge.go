package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type testReportProvenance struct {
	SchemaVersion int    `json:"schema_version"`
	SourceSHA256  string `json:"source_report_sha256"`
	SourceName    string `json:"source_name"`
	ReceiptIndex  int    `json:"source_receipt_index"`
}

type testReportMergeState struct {
	RunIDs     map[string]string
	Sources    map[string]bool
	Origins    map[string]bool
	InputBytes int
	Nodes      int
}

func cmdTestReport(args []string) int {
	if len(args) == 0 || args[0] != "merge" {
		return printErr("Invalid report command", errors.New("use test report merge --output merged.json batch-a.json batch-b.json"))
	}
	fs := newFlagSet("test report merge", flag.ContinueOnError)
	output := fs.String("output", "", "write a merged raw JSON receipt array")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if *output == "" || fs.NArg() < 2 || fs.NArg() > 100 {
		return printErr("Invalid merge options", errors.New("--output and 2..100 input report paths are required; flags must precede input paths"))
	}
	if err := mergeTestReports(fs.Args(), *output); err != nil {
		return printErr("Could not merge test reports", err)
	}
	return 0
}

func mergeTestReports(paths []string, output string) error {
	state := testReportMergeState{RunIDs: make(map[string]string), Sources: make(map[string]bool), Origins: make(map[string]bool)}
	merged := make([]json.RawMessage, 0)
	for _, path := range paths {
		body, err := readTestReportMergeInput(path, output, &state)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(body)
		hash := hex.EncodeToString(digest[:])
		if state.Sources[hash] {
			return fmt.Errorf("duplicate input report content: %q", path)
		}
		state.Sources[hash] = true
		var roots []json.RawMessage
		decoder := json.NewDecoder(bytes.NewReader(body))
		if err := decoder.Decode(&roots); err != nil {
			return fmt.Errorf("decode report %q: %w", path, err)
		}
		if decoder.Decode(new(any)) != io.EOF || len(roots) == 0 {
			return fmt.Errorf("report %q must contain exactly one nonempty JSON receipt array", path)
		}
		for index, raw := range roots {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
				return fmt.Errorf("report %q receipt %d must be an object", path, index)
			}
			var receipt testRunReceipt
			if err := json.Unmarshal(raw, &receipt); err != nil {
				return fmt.Errorf("decode receipt %q/%d: %w", path, index, err)
			}
			if err := state.checkReceipt(receipt, fmt.Sprintf("%s/%d", path, index), 0); err != nil {
				return err
			}
			provenance := receipt.ReportProvenance
			if len(provenance) >= 64 {
				return fmt.Errorf("receipt %q/%d provenance exceeds merge history limit", path, index)
			}
			provenance = append(provenance, testReportProvenance{SchemaVersion: 1, SourceSHA256: hash, SourceName: filepath.Base(path), ReceiptIndex: index})
			for _, source := range provenance {
				decoded, err := hex.DecodeString(source.SourceSHA256)
				if source.SchemaVersion != 1 || err != nil || len(decoded) != 32 || source.ReceiptIndex < 0 {
					return fmt.Errorf("receipt %q/%d has unsupported or invalid provenance", path, index)
				}
				key := fmt.Sprintf("%x/%d", decoded, source.ReceiptIndex)
				if state.Origins[key] {
					return fmt.Errorf("duplicate source receipt provenance in %q/%d", path, index)
				}
				state.Origins[key] = true
			}
			encoded, err := json.Marshal(provenance)
			if err != nil {
				return err
			}
			fields["report_provenance"] = encoded
			encoded, err = json.Marshal(fields)
			if err != nil {
				return err
			}
			merged = append(merged, encoded)
		}
	}
	body, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	if len(body) > testBaselineReportLimit {
		return errors.New("merged report exceeds 64 MiB; merge fewer batches")
	}
	return writeTestMergedReport(output, body)
}

func readTestReportMergeInput(path, output string, state *testReportMergeState) ([]byte, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if destination, err := os.Stat(output); err == nil {
		if os.SameFile(info, destination) {
			return nil, errors.New("merge output must not overwrite any input report")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	remaining := testBaselineReportLimit - state.InputBytes
	body, err := io.ReadAll(io.LimitReader(file, int64(remaining)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > remaining {
		return nil, errors.New("combined merge inputs exceed 64 MiB")
	}
	state.InputBytes += len(body)
	return body, nil
}

func (state *testReportMergeState) checkReceipt(r testRunReceipt, location string, depth int) error {
	state.Nodes++
	if depth > 32 || state.Nodes > 100000 {
		return errors.New("receipt hierarchy exceeds merge depth or node limit")
	}
	if r.Scenario == "" || r.Engine == "" || r.Profile == "" || (r.Status != "passed" && r.Status != "failed" && r.Status != "skipped") {
		return fmt.Errorf("receipt %q lacks supported scenario/engine/profile/status metadata", location)
	}
	if r.RunID != "" {
		if previous, exists := state.RunIDs[r.RunID]; exists {
			return fmt.Errorf("duplicate run ID %q in %s and %s", r.RunID, previous, location)
		}
		state.RunIDs[r.RunID] = location
	}
	if r.FaultMatrix != nil && r.Resilience != nil {
		return fmt.Errorf("receipt %q has ambiguous matrix and resilience hierarchies", location)
	}
	if r.FaultMatrix != nil {
		if len(r.FaultMatrix.Cases) == 0 {
			return fmt.Errorf("receipt %q has an empty matrix", location)
		}
		for i, child := range r.FaultMatrix.Cases {
			if err := state.checkReceipt(child, fmt.Sprintf("%s/matrix/%d", location, i), depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if r.Resilience != nil {
		if len(r.Resilience.History) == 0 {
			return fmt.Errorf("receipt %q has an empty resilience history", location)
		}
		for i, child := range r.Resilience.History {
			if err := state.checkReceipt(child, fmt.Sprintf("%s/history/%d", location, i), depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if r.Status != "skipped" && r.RunID == "" {
		return fmt.Errorf("executed receipt %q has no stable run ID; cannot prove it is distinct from other batches", location)
	}
	return nil
}

func writeTestMergedReport(path string, body []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".gregale-report-merge-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() { _ = os.Remove(temporary) }()
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
