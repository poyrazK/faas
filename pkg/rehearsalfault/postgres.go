// Package rehearsalfault defines scoped dependency fault evidence (ADR-20261018000200).
package rehearsalfault

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// These are evidence-format bounds, not account quotas.
const MaxUnavailableMS int64 = 30000
const MaxRecoveryMS int64 = 120000

var name = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
var digest = regexp.MustCompile(`^[a-f0-9]{64}$`)
var run = regexp.MustCompile(`^[a-f0-9]{32}$`)

var ErrNotFound = errors.New("PostgreSQL fault not found")

type Postgres struct {
	Workload        string `json:"workload" yaml:"workload"`
	Checkpoint      string `json:"checkpoint" yaml:"checkpoint"`
	UnavailableMS   int64  `json:"unavailable_ms" yaml:"unavailable_ms"`
	RecoverWithinMS int64  `json:"recover_within_ms" yaml:"recover_within_ms"`
}

func (s Postgres) Validate() error {
	if !name.MatchString(s.Workload) || !name.MatchString(s.Checkpoint) || s.UnavailableMS < 100 || s.UnavailableMS > MaxUnavailableMS || s.RecoverWithinMS < 1 || s.RecoverWithinMS > MaxRecoveryMS {
		return errors.New("postgres_fault needs a named worker/checkpoint, unavailable_ms 100..30000 and recover_within_ms 1..120000")
	}
	return nil
}

type Receipt struct {
	ID                  string   `json:"id"`
	AccountID           string   `json:"account_id"`
	RunID               string   `json:"run_id"`
	AppID               string   `json:"app_id"`
	ResourceID          string   `json:"resource_id"`
	DeploymentID        string   `json:"deployment_id"`
	InstanceID          string   `json:"instance_id"`
	RecorderSessionID   string   `json:"recorder_session_id"`
	InvocationID        string   `json:"invocation_id"`
	CheckpointAttemptID string   `json:"checkpoint_attempt_id"`
	OwnerID             string   `json:"owner_id"`
	BackendSHA256       string   `json:"backend_sha256"`
	PlanSHA256          string   `json:"plan_sha256"`
	Spec                Postgres `json:"spec"`
	State               string   `json:"state"`
	Activated           bool     `json:"activated"`
	Recovered           bool     `json:"recovered"`
	ClosedConnections   int64    `json:"closed_connections"`
	OutageMS            int64    `json:"outage_ms"`
	RecoveryMS          int64    `json:"recovery_ms"`
	Error               string   `json:"error,omitempty"`
}

func (r Receipt) Validate() error {
	if r.Spec.Validate() != nil || !run.MatchString(r.RunID) || !digest.MatchString(r.PlanSHA256) || !digest.MatchString(r.BackendSHA256) || r.State != "recovered" || r.Error != "" || !r.Activated || !r.Recovered || r.OutageMS < r.Spec.UnavailableMS || r.RecoveryMS < 0 || r.ClosedConnections < 0 {
		return errors.New("PostgreSQL fault activation or recovery evidence is incomplete")
	}
	for _, id := range []string{r.ID, r.AccountID, r.AppID, r.ResourceID, r.DeploymentID, r.InstanceID, r.RecorderSessionID, r.InvocationID, r.CheckpointAttemptID, r.OwnerID} {
		u, err := uuid.Parse(id)
		if err != nil || u == uuid.Nil {
			return errors.New("PostgreSQL fault identity is incomplete")
		}
	}
	return nil
}

// Row is private, durable recovery state; clocks are not equality observations.
type Row struct {
	Receipt
	CreatedAt  time.Time `json:"created_at"`
	RestoreAt  time.Time `json:"restore_at"`
	DeadlineAt time.Time `json:"deadline_at"`
}

type Catalog interface {
	ScenarioPostgresFault(context.Context, string) (Row, error)
	ScenarioPostgresFaultForRun(context.Context, string, string) (Row, error)
	PendingScenarioPostgresFaults(context.Context) ([]Row, error)
	TransitionScenarioPostgresFault(context.Context, Row, string) error
}

func Clone(r Row) Row { b, _ := json.Marshal(r); var v Row; _ = json.Unmarshal(b, &v); return v }

func Transition(old, next Row) bool {
	a, b := old.Receipt, next.Receipt
	a.State, b.State = "", ""
	a.Activated, b.Activated, a.Recovered, b.Recovered = false, false, false, false
	a.ClosedConnections, b.ClosedConnections, a.OutageMS, b.OutageMS, a.RecoveryMS, b.RecoveryMS = 0, 0, 0, 0, 0, 0
	a.Error, b.Error = "", ""
	if a != b || !old.CreatedAt.Equal(next.CreatedAt) || next.DeadlineAt.Before(old.DeadlineAt) || !old.RestoreAt.IsZero() && !old.RestoreAt.Equal(next.RestoreAt) || old.Error != "" && old.Error != next.Error || old.Recovered && !next.Recovered || old.Activated && !next.Activated {
		return false
	}
	if next.State == "active" && (!next.Activated || next.Recovered || next.RestoreAt.IsZero()) || next.State == "cutting" && (next.Activated || next.Recovered) {
		return false
	}
	allowed := map[string][]string{"requested": {"cutting", "recovering"}, "cutting": {"active", "recovering"}, "active": {"recovering"}, "recovering": {"recovered", "failed"}, "failed": {"recovering"}, "recovered": {"failed"}}
	for _, state := range allowed[old.State] {
		if state == next.State {
			return next.State != "recovered" || next.Validate() == nil
		}
	}
	return false
}
