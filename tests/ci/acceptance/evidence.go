package acceptance

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Scenario struct {
	ExecutionID string            `json:"execution_id"`
	Status      string            `json:"status"`
	Checks      map[string]bool   `json:"checks"`
	Attempts    map[string]string `json:"attempts"`
	Instances   map[string]string `json:"instances"`
	MBSeconds   int64             `json:"mb_seconds"`
}

type RetiredObject struct {
	PhysicalKey string `json:"physical_key"`
	Digest      string `json:"digest"`
	Bytes       int64  `json:"bytes"`
}

// Journal contains no bearer, capability, DSN, private key, source or output body.
// It survives failure so an operator can resume cleanup without deleting intent.
type Journal struct {
	Version     int                 `json:"version"`
	RunID       string              `json:"run_id"`
	Schema      string              `json:"schema"`
	DatabaseID  string              `json:"database_id"`
	SourceSHA   string              `json:"source_sha"`
	RunnerSHA   string              `json:"runner_sha256"`
	GuestSHA    string              `json:"guest_sha256"`
	Image       string              `json:"image"`
	Origin      string              `json:"origin"`
	Bucket      string              `json:"bucket"`
	AccountID   string              `json:"account_id"`
	HarnessDir  string              `json:"harness_dir,omitempty"`
	StartedAt   time.Time           `json:"started_at"`
	Scenarios   map[string]Scenario `json:"scenarios"`
	Retired     []RetiredObject     `json:"retired_objects,omitempty"`
	Cleanup     bool                `json:"cleanup_confirmed"`
	NativeLeaks bool                `json:"native_leakcheck_passed"`
	SchemaGone  bool                `json:"schema_removed"`
	FilesGone   bool                `json:"native_files_removed"`
}

func NewJournal(c Config, databaseID string) Journal {
	id := uuid.New()
	return Journal{Version: 1, RunID: id.String(), Schema: "ci_acceptance_" + compactID(id.String()),
		DatabaseID: databaseID, SourceSHA: c.SourceSHA, RunnerSHA: c.RunnerSHA, GuestSHA: c.GuestSHA,
		Image: c.Image, Origin: c.Origin, Bucket: c.Bucket, StartedAt: time.Now().UTC(), Scenarios: make(map[string]Scenario)}
}

func (j Journal) Valid() bool {
	return j.Version == 1 && uuid.Validate(j.RunID) == nil && schemaName.MatchString(j.Schema) &&
		j.Schema == "ci_acceptance_"+compactID(j.RunID) && hexSHA.MatchString(j.DatabaseID) &&
		sourceSHA.MatchString(j.SourceSHA) && hexSHA.MatchString(j.RunnerSHA) && hexSHA.MatchString(j.GuestSHA) &&
		(j.AccountID == "" || uuid.Validate(j.AccountID) == nil)
}

func compactID(id string) string {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return ""
	}
	return strings.ReplaceAll(parsed.String(), "-", "")
}

func WriteJournal(dir string, j Journal) error {
	if !j.Valid() {
		return errors.New("invalid CI acceptance recovery journal")
	}
	if err := privateDirectory(dir); err != nil {
		return err
	}
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".journal-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err = file.Write(append(data, '\n')); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(file.Name(), filepath.Join(dir, "journal.json")); err != nil {
		return err
	}
	parent, err := os.Open(dir) //nolint:forbidigo // Fsync the validated absolute private fixture directory; no customer file is read.
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	return parent.Sync()
}

func ReadJournal(dir string) (Journal, error) {
	if err := privateDirectory(dir); err != nil {
		return Journal{}, err
	}
	path := filepath.Join(dir, "journal.json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > 1<<20 {
		return Journal{}, errors.New("CI acceptance journal must be a bounded private regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Journal{}, err
	}
	var j Journal
	if json.Unmarshal(data, &j) != nil || !j.Valid() {
		return Journal{}, errors.New("invalid CI acceptance recovery journal")
	}
	return j, nil
}

func privateDirectory(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 || !filepath.IsAbs(dir) {
		return errors.New("CI acceptance evidence requires an absolute private directory")
	}
	return nil
}

func Verdict(j Journal) error {
	if !j.Valid() || j.AccountID == "" || !j.Cleanup || !j.NativeLeaks || !j.SchemaGone || !j.FilesGone || len(j.Retired) == 0 {
		return errors.New("CI acceptance evidence or confirmed cleanup is incomplete")
	}
	for _, object := range j.Retired {
		parts := strings.Split(object.PhysicalKey, "/")
		if len(parts) != 3 || parts[0] != "ci-v1" || parts[1] != j.AccountID || uuid.Validate(parts[2]) != nil ||
			!strings.HasPrefix(object.Digest, "sha256:") || !hexSHA.MatchString(strings.TrimPrefix(object.Digest, "sha256:")) || object.Bytes < 0 {
			return errors.New("invalid CI retirement evidence")
		}
	}
	seenAttempts, seenInstances := make(map[string]bool), make(map[string]bool)
	for _, name := range ScenarioNames() {
		s, exists := j.Scenarios[name]
		if !exists || s.Status != "passed" || uuid.Validate(s.ExecutionID) != nil {
			return errors.New("CI acceptance scenario is missing or failed: " + name)
		}
		checks := []string{"native_stop", "one_attempt", "image_snapshot", "expected_result", "http_admission", "http_status", "http_logs", "worker_progress"}
		switch name {
		case "pipeline":
			checks = append(checks, "two_jobs", "ordered_dependencies", "verified_download", "large_artifact", "billing")
		case "restart":
			checks = append(checks, "apid_restarted", "schedd_restarted", "controller_restarted", "lost_ack", "verified_download")
		case "wake_during_ci":
			checks = append(checks, "concurrent_ci", "tenant_wake")
		}
		for _, check := range checks {
			if !s.Checks[check] {
				return errors.New("CI acceptance check is missing: " + name + "/" + check)
			}
		}
		if len(s.Attempts) == 0 || len(s.Instances) != len(s.Attempts) || name == "pipeline" && (len(s.Attempts) != 2 || s.MBSeconds <= 0) {
			return errors.New("CI native attempt or metering evidence is incomplete: " + name)
		}
		for job, attempt := range s.Attempts {
			if uuid.Validate(attempt) != nil || uuid.Validate(s.Instances[job]) != nil || seenInstances[s.Instances[job]] || seenAttempts[attempt] {
				return errors.New("invalid or reused CI native identity")
			}
			seenInstances[s.Instances[job]], seenAttempts[attempt] = true, true
		}
	}
	return nil
}
