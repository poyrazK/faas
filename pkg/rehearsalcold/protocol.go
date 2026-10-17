// Package rehearsalcold defines bounded cold-start evidence (ADR-20261017201013).
package rehearsalcold

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

// MaxWithinMS bounds the evidence protocol; it is not an account quota.
const MaxWithinMS int64 = 120000

type Spec struct {
	Version  int   `json:"version" yaml:"version"`
	WithinMS int64 `json:"within_ms" yaml:"within_ms"`
}

type Preparation struct {
	ID                    string `json:"id"`
	AccountID             string `json:"account_id"`
	AppID                 string `json:"app_id"`
	RunID                 string `json:"run_id"`
	Workload              string `json:"workload"`
	DeploymentID          string `json:"deployment_id"`
	InstanceID            string `json:"instance_id,omitempty"`
	RecorderSessionID     string `json:"recorder_session_id,omitempty"`
	State                 string `json:"state"`
	BootInstanceID        string `json:"boot_instance_id,omitempty"`
	BootRecorderSessionID string `json:"boot_recorder_session_id,omitempty"`
	WakeID                string `json:"wake_id,omitempty"`
	Method                string `json:"method,omitempty"`
	StartupMS             int64  `json:"startup_ms"`
}

type Evidence struct {
	Version      int           `json:"version"`
	RunID        string        `json:"run_id"`
	Spec         Spec          `json:"spec"`
	PlanSHA256   string        `json:"plan_sha256"`
	Status       string        `json:"status"`
	Preparations []Preparation `json:"preparations"`
}

var workloadPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$`)
var runPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func Normalize(spec Spec, workloads []string) ([]byte, string, error) {
	if spec.Version != Version || spec.WithinMS < 1 || spec.WithinMS > MaxWithinMS || len(workloads) < 1 || len(workloads) > 16 {
		return nil, "", errors.New("cold start requires version 1 and within_ms from 1 to 120000")
	}
	names := slices.Clone(workloads)
	slices.Sort(names)
	for i, name := range names {
		if !workloadPattern.MatchString(name) || i > 0 && names[i-1] == name {
			return nil, "", errors.New("invalid cold-start workload inventory")
		}
	}
	body, _ := json.Marshal(spec)
	plan, _ := json.Marshal(struct {
		Spec      Spec     `json:"spec"`
		Workloads []string `json:"workloads"`
	}{spec, names})
	sum := sha256.Sum256(plan)
	return body, hex.EncodeToString(sum[:]), nil
}

func Decode(raw string) Spec { var s Spec; _ = json.Unmarshal([]byte(raw), &s); return s }

func validID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil
}

func canonicalID(id string) string { u, _ := uuid.Parse(id); return u.String() }

func (e *Evidence) Validate(workloads []string) error {
	if e == nil || e.Version != Version || e.Status != "complete" || !runPattern.MatchString(e.RunID) || len(e.Preparations) != len(workloads) {
		return errors.New("cold-start evidence is incomplete")
	}
	_, digest, err := Normalize(e.Spec, workloads)
	if err != nil || digest != e.PlanSHA256 {
		return errors.New("cold-start plan differs")
	}
	names, ids := map[string]bool{}, map[string]bool{}
	account := ""
	for _, name := range workloads {
		names[name] = true
	}
	for _, p := range e.Preparations {
		if !names[p.Workload] || p.RunID != e.RunID || p.State != "booted" || p.Method != "cold_boot" || p.StartupMS < 0 || !validID(p.WakeID) {
			return errors.New("cold-start boot proof is missing or differs")
		}
		delete(names, p.Workload)
		for _, id := range []string{p.ID, p.AccountID, p.AppID, p.DeploymentID, p.BootInstanceID, p.BootRecorderSessionID} {
			if !validID(id) {
				return errors.New("cold-start identity is invalid")
			}
		}
		currentAccount := canonicalID(p.AccountID)
		if account != "" && account != currentAccount {
			return errors.New("cold-start account inventory differs")
		}
		account = currentAccount
		for _, id := range []string{p.ID, p.AppID, p.BootInstanceID, p.BootRecorderSessionID} {
			key := canonicalID(id)
			if ids[key] {
				return errors.New("cold-start identity is reused")
			}
			ids[key] = true
		}
		if p.InstanceID != "" || p.RecorderSessionID != "" {
			if !validID(p.InstanceID) || !validID(p.RecorderSessionID) || canonicalID(p.InstanceID) == canonicalID(p.BootInstanceID) || canonicalID(p.RecorderSessionID) == canonicalID(p.BootRecorderSessionID) {
				return errors.New("cold-start retirement identity differs")
			}
		}
	}
	return nil
}

// Store is optional. Unknown runtimes never fall back to an ordinary destroy.
type Store interface {
	BeginScenarioColdPreparation(context.Context, string, string) (Preparation, error)
	ScenarioColdPreparation(context.Context, string) (Preparation, error)
	ClaimScenarioColdPreparation(context.Context, string) (Preparation, error)
	CompleteScenarioColdPreparation(context.Context, string, string) error
	FailScenarioColdPreparation(context.Context, string) error
}
