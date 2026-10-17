// Package rehearsalresume defines in-place lifecycle evidence (ADR-20261017201014).
package rehearsalresume

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"slices"

	"github.com/google/uuid"
)

const Version = 1

// ErrCheckpointBusy permits the original held relay to wait for the already
// running trigger command to retire. It grants no lifecycle authority.
var ErrCheckpointBusy = errors.New("running-job checkpoint awaits command retirement")

// Bounds on the evidence protocol, not account entitlements.
const (
	MaxPauseMS  int64 = 30000
	MaxWithinMS int64 = 120000
)

type Spec struct {
	Version          int      `json:"version"`
	Checkpoint       string   `json:"checkpoint"`
	Workloads        []string `json:"workloads"`
	PauseMS          int64    `json:"pause_ms"`
	WithinMS         int64    `json:"within_ms"`
	WorkerCheckpoint string   `json:"worker_checkpoint,omitempty"`
}

type Receipt struct {
	ID                  string `json:"id"`
	AccountID           string `json:"account_id"`
	AppID               string `json:"app_id"`
	RunID               string `json:"run_id"`
	Workload            string `json:"workload"`
	DeploymentID        string `json:"deployment_id"`
	InstanceID          string `json:"instance_id"`
	RecorderSessionID   string `json:"recorder_session_id"`
	State               string `json:"state"`
	Method              string `json:"method,omitempty"`
	PauseMS             int64  `json:"pause_ms"`
	ResumeMS            int64  `json:"resume_ms"`
	InvocationID        string `json:"invocation_id,omitempty"`
	InvocationAttempt   int    `json:"invocation_attempt,omitempty"`
	CheckpointAttemptID string `json:"checkpoint_attempt_id,omitempty"`
}

type Evidence struct {
	Version    int       `json:"version"`
	RunID      string    `json:"run_id"`
	Spec       Spec      `json:"spec"`
	PlanSHA256 string    `json:"plan_sha256"`
	Status     string    `json:"status"`
	Receipts   []Receipt `json:"receipts"`
}

var workloadPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$`)
var runPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var checkpointPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

func (s Spec) ManagedJob() bool { return s.Checkpoint == "managed_job" }

func Normalize(spec Spec, inventory []string) ([]byte, string, error) {
	if spec.Version != Version || (spec.Checkpoint != "after_setup" && !spec.ManagedJob()) || spec.PauseMS < 1 || spec.PauseMS > MaxPauseMS || spec.WithinMS < 1 || spec.WithinMS > MaxWithinMS || len(spec.Workloads) < 1 || len(spec.Workloads) > 16 || len(inventory) < 1 || len(inventory) > 16 {
		return nil, "", errors.New("suspend/resume requires version 1, after_setup or managed_job, selected workloads, pause_ms 1..30000 and within_ms 1..120000")
	}
	if spec.ManagedJob() && (len(spec.Workloads) != 1 || !checkpointPattern.MatchString(spec.WorkerCheckpoint)) || !spec.ManagedJob() && spec.WorkerCheckpoint != "" {
		return nil, "", errors.New("managed_job requires one workload and a named worker_checkpoint")
	}
	names := slices.Clone(inventory)
	slices.Sort(names)
	for i, name := range names {
		if !workloadPattern.MatchString(name) || i > 0 && names[i-1] == name {
			return nil, "", errors.New("invalid suspend/resume inventory")
		}
	}
	spec.Workloads = slices.Clone(spec.Workloads)
	slices.Sort(spec.Workloads)
	for i, name := range spec.Workloads {
		if !slices.Contains(names, name) || i > 0 && spec.Workloads[i-1] == name {
			return nil, "", errors.New("suspend/resume workload is missing or repeated")
		}
	}
	raw, _ := json.Marshal(spec)
	plan, _ := json.Marshal(struct {
		Spec      Spec     `json:"spec"`
		Inventory []string `json:"inventory"`
	}{spec, names})
	sum := sha256.Sum256(plan)
	return raw, hex.EncodeToString(sum[:]), nil
}

func Decode(raw string) Spec { var s Spec; _ = json.Unmarshal([]byte(raw), &s); return s }

func (e *Evidence) Validate(inventory []string) error {
	if e == nil || e.Version != Version || e.Status != "complete" || !runPattern.MatchString(e.RunID) || len(e.Receipts) != len(e.Spec.Workloads) {
		return errors.New("suspend/resume evidence is incomplete")
	}
	_, digest, err := Normalize(e.Spec, inventory)
	if err != nil || digest != e.PlanSHA256 {
		return errors.New("suspend/resume plan differs")
	}
	names, ids := map[string]bool{}, map[uuid.UUID]bool{}
	for _, n := range e.Spec.Workloads {
		names[n] = true
	}
	var account uuid.UUID
	for _, r := range e.Receipts {
		if !names[r.Workload] || r.RunID != e.RunID || r.State != "complete" || r.Method != "in_place" || r.PauseMS < e.Spec.PauseMS || r.ResumeMS < 0 {
			return errors.New("suspend/resume owner proof is missing or differs")
		}
		delete(names, r.Workload)
		identities := []string{r.ID, r.AccountID, r.AppID, r.DeploymentID, r.InstanceID, r.RecorderSessionID}
		if e.Spec.ManagedJob() {
			if r.InvocationAttempt != 1 {
				return errors.New("managed checkpoint first dispatch proof is missing")
			}
			identities = append(identities, r.InvocationID, r.CheckpointAttemptID)
		} else if r.InvocationID != "" || r.InvocationAttempt != 0 || r.CheckpointAttemptID != "" {
			return errors.New("unexpected managed checkpoint identity")
		}
		for _, id := range identities {
			u, parseErr := uuid.Parse(id)
			if parseErr != nil || u == uuid.Nil {
				return errors.New("invalid suspend/resume identity")
			}
		}
		a, _ := uuid.Parse(r.AccountID)
		if account != uuid.Nil && a != account {
			return errors.New("suspend/resume account inventory differs")
		}
		account = a
		for _, id := range []string{r.ID, r.AppID, r.InstanceID, r.RecorderSessionID} {
			u, _ := uuid.Parse(id)
			if ids[u] {
				return errors.New("suspend/resume identity is reused")
			}
			ids[u] = true
		}
	}
	return nil
}

// Store is optional. An unsupported owner cannot substitute ordinary freeze/thaw.
type Store interface {
	BeginScenarioSuspendResume(context.Context, string, string) (Evidence, error)
	ScenarioSuspendResumeEvidence(context.Context, string, string) (Evidence, error)
	ScenarioSuspendResume(context.Context, string) (Receipt, error)
	PendingScenarioSuspendResumes(context.Context) ([]Receipt, error)
	ClaimScenarioSuspendResume(context.Context, string) (Receipt, error)
	CompleteScenarioSuspendResume(context.Context, string) error
	FailScenarioSuspendResume(context.Context, string) error
}
