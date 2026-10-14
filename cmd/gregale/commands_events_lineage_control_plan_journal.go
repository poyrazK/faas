package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
)

// Each resumed run writes a new journal, preserving earlier evidence.
type lineageControlPlanJournalHeader struct {
	Format     string `json:"format"`
	Version    int    `json:"version"`
	PlanSHA256 string `json:"plan_sha256"`
}
type lineageControlPlanJournalRecord struct {
	Index  int                      `json:"index"`
	Result lineageControlPlanResult `json:"result"`
}
type lineageControlPlanJournal struct {
	file  *os.File
	bytes int
}
type lineageControlPlanRun struct {
	journal  *lineageControlPlanJournal
	previous map[int]lineageControlPlanResult
}

func lineageControlPlanDigest(plan lineageControlPlan) string {
	raw, _ := json.Marshal(plan)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func newLineageControlPlanJournal(path string, plan lineageControlPlan) (*lineageControlPlanJournal, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	j := &lineageControlPlanJournal{file: f}
	if err = j.append(lineageControlPlanJournalHeader{Format: "gregale.event-lineage-control-execution", Version: 1, PlanSHA256: lineageControlPlanDigest(plan)}); err != nil {
		_ = f.Close()
		return nil, err
	}
	dir, err := openCustomerDirectoryForSync(filepath.Dir(path))
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	err = dir.Sync()
	closeErr := dir.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return j, nil
}
func (j *lineageControlPlanJournal) append(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if j.bytes+len(raw) > api.EventExecutionLineageControlReportMaxBytes {
		return fmt.Errorf("execution report exceeds byte limit")
	}
	n, err := j.file.Write(raw)
	if err != nil {
		return err
	}
	if n != len(raw) {
		return io.ErrShortWrite
	}
	j.bytes += n
	return j.file.Sync()
}
func decodeLineageControlJournalLine(raw []byte, target any) error {
	if err := rejectDuplicateLineageJSON(json.NewDecoder(bytes.NewReader(raw))); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("report line must contain one JSON object")
	}
	return nil
}
func readLineageControlPlanJournal(path string, plan lineageControlPlan) (map[int]lineageControlPlanResult, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > int64(api.EventExecutionLineageControlReportMaxBytes) {
		return nil, fmt.Errorf("report must be a bounded regular file")
	}
	f, err := openCustomerFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err = f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > int64(api.EventExecutionLineageControlReportMaxBytes) {
		return nil, fmt.Errorf("report must be a bounded regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, int64(api.EventExecutionLineageControlReportMaxBytes+1)))
	if err != nil {
		return nil, err
	}
	if len(raw) > api.EventExecutionLineageControlReportMaxBytes {
		return nil, fmt.Errorf("report exceeds byte limit")
	}
	// A crash may interrupt only the final append. Never trust that partial record.
	last := bytes.LastIndexByte(raw, '\n')
	if last < 0 {
		return nil, fmt.Errorf("report has no complete header")
	}
	scanner := bufio.NewScanner(bytes.NewReader(raw[:last+1]))
	scanner.Buffer(make([]byte, 4096), api.EventExecutionLineageControlReportMaxBytes)
	if !scanner.Scan() {
		return nil, fmt.Errorf("missing report header")
	}
	if _, err = lineageHistoryObjectFields(scanner.Bytes(), []string{"format", "version", "plan_sha256"}, nil); err != nil {
		return nil, err
	}
	var header lineageControlPlanJournalHeader
	if err = decodeLineageControlJournalLine(scanner.Bytes(), &header); err != nil {
		return nil, err
	}
	if header.Format != "gregale.event-lineage-control-execution" || header.Version != 1 || header.PlanSHA256 != lineageControlPlanDigest(plan) {
		return nil, fmt.Errorf("execution report does not match unchanged normalized plan")
	}
	out := make(map[int]lineageControlPlanResult)
	count := 0
	lastIndex := -1
	perEntry := make(map[int]int)
	for scanner.Scan() {
		count++
		if count > 2*len(plan.Actions) {
			return nil, fmt.Errorf("too many report records")
		}
		fields, shapeErr := lineageHistoryObjectFields(scanner.Bytes(), []string{"index", "result"}, nil)
		if shapeErr != nil {
			return nil, shapeErr
		}
		if _, shapeErr = lineageHistoryObjectFields(fields["result"], []string{"job_request_id", "command_id", "action", "outcome"}, []string{"error", "status", "processed", "selected", "receipt", "evidence"}); shapeErr != nil {
			return nil, shapeErr
		}
		var record lineageControlPlanJournalRecord
		if err = decodeLineageControlJournalLine(scanner.Bytes(), &record); err != nil {
			return nil, err
		}
		if record.Index < 0 || record.Index >= len(plan.Actions) {
			return nil, fmt.Errorf("invalid report entry index")
		}
		if record.Index < lastIndex || perEntry[record.Index] >= 2 {
			return nil, fmt.Errorf("report records are out of order or duplicated")
		}
		if prior, exists := out[record.Index]; exists && prior.Outcome != "unconfirmed" {
			return nil, fmt.Errorf("only an uncertain intent may have a final record")
		}
		lastIndex = record.Index
		perEntry[record.Index]++
		entry := plan.Actions[record.Index]
		result := record.Result
		if result.JobRequestID != entry.JobRequestID || result.CommandID != entry.CommandID || result.Action != entry.Action || (result.Outcome != "acknowledged" && result.Outcome != "unconfirmed" && result.Outcome != "not_attempted") {
			return nil, fmt.Errorf("report entry does not match plan")
		}
		if result.Evidence != "" && result.Evidence != "receipt_present" && result.Evidence != "receipt_absent" && result.Evidence != "command_conflict" && result.Evidence != "unavailable" && result.Evidence != "expired" && result.Evidence != "stale_guard" {
			return nil, fmt.Errorf("unknown execution report evidence")
		}
		// Stored acknowledgments are evidence only; resume verifies them with GETs.
		out[record.Index] = result
	}
	return out, scanner.Err()
}
