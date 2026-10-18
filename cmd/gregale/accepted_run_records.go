package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type acceptedRunRecord struct {
	Version    int    `json:"version"`
	Kind       string `json:"kind"`
	RunID      string `json:"run_id"`
	Name       string `json:"name"`
	App        string `json:"app,omitempty"`
	AppID      string `json:"app_id,omitempty"`
	JobID      string `json:"job_id,omitempty"`
	AccountID  string `json:"account_id,omitempty"`
	Profile    string `json:"profile"`
	API        string `json:"api_base"`
	RecordedAt string `json:"recorded_at,omitempty"`
}

func acceptedRunDirectory() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("read current directory: %w", err)
	}
	root, err := projectContextRoot(cwd)
	if err != nil {
		return "", fmt.Errorf("locate run record root: %w", err)
	}
	return filepath.Join(root, ".gregale", "accepted-runs"), nil
}

func validateAcceptedRunRecord(record acceptedRunRecord) error {
	if record.RecordedAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, record.RecordedAt); err != nil {
			return errors.New("invalid run recording time")
		}
	}
	if record.Version != 1 || !jobRunIDPattern.MatchString(record.RunID) {
		return errors.New("invalid accepted run record version or UUID")
	}
	if err := validateProfileName(record.Profile); err != nil {
		return fmt.Errorf("invalid recorded profile: %w", err)
	}
	if _, err := validateConfigAPIBase(record.API); err != nil {
		return errors.New("invalid recorded API address")
	}
	switch record.Kind {
	case "job":
		if !jobSlugPattern.MatchString(record.Name) || record.JobID == "" || record.AccountID == "" {
			return errors.New("incomplete accepted Job identity")
		}
	case "workflow":
		if (record.App != "" && !api.ValidAppSlug(record.App)) || (record.App == "" && record.AppID == "") || record.Name == "" || len(record.Name) > api.WorkflowWebhookNameMaxBytes {
			return errors.New("incomplete accepted Workflow identity")
		}
	default:
		return errors.New("unknown accepted run kind")
	}
	return nil
}

// One immutable record per identity avoids a shared read/modify/write journal.
// Failures to save must never turn an accepted mutation into a failed submission.
func rememberAcceptedRun(record acceptedRunRecord) {
	record.Version, record.Profile, record.API = 1, currentProfile(), apiBase()
	record.RecordedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := saveAcceptedRunRecord(record); err != nil {
		_, _ = fmt.Fprintf(osStderr, "Run %s was accepted, but its local resume record could not be saved: %s. Keep the run ID; do not resubmit to repair this local failure.\n", oneLine(record.RunID), compactInspectFailureText(err.Error()))
	}
}

func rememberAcceptedJob(name string, run api.JobRunResponse) {
	rememberAcceptedRun(acceptedRunRecord{Kind: "job", Name: name, RunID: run.ID, JobID: run.JobID, AccountID: run.AccountID})
}

func saveAcceptedRunRecord(record acceptedRunRecord) error {
	if err := validateAcceptedRunRecord(record); err != nil {
		return err
	}
	directory, err := acceptedRunDirectory()
	if err != nil {
		return err
	}
	for _, dir := range []string{filepath.Dir(directory), directory} {
		if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("create resume directory: %w", err)
		}
		info, err := os.Lstat(dir)
		if err != nil {
			return fmt.Errorf("inspect resume directory: %w", err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("resume directory must be a real directory")
		}
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode run record: %w", err)
	}
	if len(data) > 8192 {
		return errors.New("run record exceeds 8 KiB")
	}
	path := filepath.Join(directory, acceptedRunRecordFilename(record))
	f, err := os.CreateTemp(directory, ".accepted-*")
	if err != nil {
		return fmt.Errorf("create run record: %w", err)
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write run record: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync run record: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close run record: %w", err)
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return fmt.Errorf("save run record: %w", err)
	}
	return nil
}

func acceptedRunRecordFilename(record acceptedRunRecord) string {
	identity := record.Kind + "\x00" + record.API + "\x00" + record.Profile + "\x00" + record.RunID
	digest := sha256.Sum256([]byte(identity))
	return fmt.Sprintf("%x.json", digest)
}

func loadAcceptedRunRecords(kind string) ([]acceptedRunRecord, error) {
	directory, err := acceptedRunDirectory()
	if err != nil {
		return nil, err
	}
	for _, dir := range []string{filepath.Dir(directory), directory} {
		info, err := os.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) {
			return []acceptedRunRecord{}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("inspect resume directory: %w", err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("resume directory must be a real directory")
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("list run records: %w", err)
	}
	records := []acceptedRunRecord{}
	scanned := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		scanned++
		if scanned > 1000 {
			return nil, errors.New("more than 1000 run records; archive old records before resuming")
		}
		f, err := openCustomerFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("open run record: %w", err)
		}
		data, err := io.ReadAll(io.LimitReader(f, 8193))
		_ = f.Close()
		if err != nil {
			return nil, fmt.Errorf("read run record: %w", err)
		}
		if len(data) > 8192 {
			return nil, errors.New("run record exceeds 8 KiB")
		}
		var record acceptedRunRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, errors.New("invalid local run record JSON")
		}
		if err := validateAcceptedRunRecord(record); err != nil {
			return nil, err
		}
		if record.Kind == kind {
			records = append(records, record)
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].RunID < records[j].RunID })
	return records, nil
}

func rememberAcceptedWorkflow(app, name string, run api.WorkflowRunResponse) {
	if name == "" {
		name = run.WorkflowName
	}
	rememberAcceptedRun(acceptedRunRecord{Kind: "workflow", App: app, AppID: run.AppID, Name: name, RunID: run.ID})
}
