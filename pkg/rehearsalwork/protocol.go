// Package rehearsalwork defines the bounded managed-async rehearsal contract.
package rehearsalwork

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
)

const (
	Version         = 1
	MaxJobs         = 64
	MaxAttempts     = 32
	MaxPayloadBytes = 8192
)

var runPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var workloadPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`)
var uuidPattern = regexp.MustCompile(`^([0-9a-f]{32}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)
var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var checkpointPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

type Plan struct {
	Version           int                `json:"version" yaml:"version"`
	Primary           string             `json:"primary" yaml:"primary"`
	Participants      []Participant      `json:"participants" yaml:"participants"`
	WorkerTermination *WorkerTermination `json:"worker_termination,omitempty" yaml:"worker_termination,omitempty"`
}

type Participant struct {
	Workload string `json:"workload" yaml:"workload"`
	Async    bool   `json:"async,omitempty" yaml:"async,omitempty"`
	Postgres string `json:"postgres,omitempty" yaml:"postgres,omitempty"`
	Objects  string `json:"objects,omitempty" yaml:"objects,omitempty"`
}

func (p Plan) Participant(name string) (Participant, bool) {
	for _, v := range p.Participants {
		if v.Workload == name {
			return v, true
		}
	}
	return Participant{}, false
}

// Normalize requires an explicit entry for every enrolled workload. Permissions
// are resource capabilities, independent of the private HTTP service namespace.
func Normalize(p Plan, workloads []string) ([]byte, string, error) {
	if p.Version != Version || len(workloads) == 0 || len(workloads) > 16 || len(p.Participants) != len(workloads) {
		return nil, "", errors.New("managed_work requires version 1 and every enrolled workload")
	}
	names := map[string]bool{}
	for _, n := range workloads {
		if !workloadPattern.MatchString(n) || names[n] {
			return nil, "", errors.New("invalid managed workload")
		}
		names[n] = true
	}
	seen, workers := map[string]bool{}, 0
	for _, v := range p.Participants {
		if !names[v.Workload] || seen[v.Workload] || (v.Postgres != "" && v.Postgres != "read_write") || (v.Objects != "" && v.Objects != "read" && v.Objects != "read_write") {
			return nil, "", errors.New("invalid managed-work participant or resource permission")
		}
		if v.Async {
			workers++
		}
		seen[v.Workload] = true
	}
	primary, ok := p.Participant(p.Primary)
	if !ok || primary.Async || workers == 0 {
		return nil, "", errors.New("managed_work needs a primary and at least one async worker")
	}
	if f := p.WorkerTermination; f != nil {
		worker, found := p.Participant(f.Workload)
		if !found || !worker.Async || !checkpointPattern.MatchString(f.Checkpoint) {
			return nil, "", errors.New("worker_termination requires a declared async worker and named checkpoint fixture")
		}
	}
	p.Participants = append([]Participant(nil), p.Participants...)
	sort.Slice(p.Participants, func(i, j int) bool { return p.Participants[i].Workload < p.Participants[j].Workload })
	b, err := json.Marshal(p)
	return b, Hash(b), err
}

func Decode(raw string) Plan { var p Plan; _ = json.Unmarshal([]byte(raw), &p); return p }
func Hash(b []byte) string   { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }
func PayloadHash(b []byte) string {
	var v any
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	if decoder.Decode(&v) != nil {
		return ""
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return Hash(raw)
}

// ObjectCredentials bind a credential to one attested workload. The base secret
// stays sealed; a sibling's credential cannot authenticate as another workload.
func ObjectCredentials(secret, app string) (string, string) {
	derive := func(kind string) string {
		h := hmac.New(sha256.New, []byte(secret))
		_, _ = h.Write([]byte("gregale-rehearsal-object-v1:" + kind + ":" + app))
		return hex.EncodeToString(h.Sum(nil))
	}
	return "GRW" + derive("access")[:29], derive("secret")
}

func PostgresRole(resource, app string) string {
	return "grpw_" + Hash([]byte(resource + ":" + app))[:40]
}

type Binding struct {
	Workload           string `json:"workload"`
	AppID              string `json:"app_id"`
	DeploymentID       string `json:"deployment_id"`
	PostgresResourceID string `json:"postgres_resource_id,omitempty"`
	ObjectsResourceID  string `json:"objects_resource_id,omitempty"`
}
type Attempt struct {
	Number       int    `json:"number"`
	InstanceID   string `json:"instance_id"`
	DeploymentID string `json:"deployment_id"`
	Outcome      string `json:"outcome"`
}
type Job struct {
	ID            string    `json:"id"`
	Workload      string    `json:"workload"`
	AppID         string    `json:"app_id"`
	DeploymentID  string    `json:"deployment_id"`
	Method        string    `json:"method"`
	Path          string    `json:"path"`
	PayloadSHA256 string    `json:"payload_sha256"`
	State         string    `json:"state"`
	Attempts      []Attempt `json:"attempts"`
}
type Evidence struct {
	Version     int          `json:"version"`
	RunID       string       `json:"run_id"`
	PlanSHA256  string       `json:"plan_sha256"`
	Status      string       `json:"status"`
	Closing     bool         `json:"closing"`
	Plan        Plan         `json:"plan"`
	Bindings    []Binding    `json:"bindings"`
	Jobs        []Job        `json:"jobs"`
	Reason      string       `json:"reason,omitempty"`
	WorkerFault *WorkerFault `json:"worker_fault,omitempty"`
}

func (e Evidence) Validate() error {
	names := make([]string, 0, len(e.Bindings))
	bindings := map[string]Binding{}
	apps := map[string]bool{}
	for _, b := range e.Bindings {
		if !validID(b.AppID) || !validID(b.DeploymentID) || apps[identifierKey(b.AppID)] {
			return errors.New("invalid managed-work binding")
		}
		apps[identifierKey(b.AppID)] = true
		names = append(names, b.Workload)
		bindings[b.Workload] = b
	}
	_, digest, err := Normalize(e.Plan, names)
	if err != nil || e.Version != Version || !runPattern.MatchString(e.RunID) || len(e.Jobs) > MaxJobs || digest != e.PlanSHA256 || !e.Closing || e.Status != "complete" || e.Reason != "" {
		return errors.New("managed work is incomplete")
	}
	for _, p := range e.Plan.Participants {
		b := bindings[p.Workload]
		if (p.Postgres != "" && !validID(b.PostgresResourceID)) || (p.Postgres == "" && b.PostgresResourceID != "") || (p.Objects != "" && !validID(b.ObjectsResourceID)) || (p.Objects == "" && b.ObjectsResourceID != "") {
			return errors.New("managed-work resource binding is missing")
		}
	}
	seen := map[string]bool{}
	workers := map[string]bool{}
	for _, j := range e.Jobs {
		p, ok := e.Plan.Participant(j.Workload)
		b := bindings[j.Workload]
		if !ok || !p.Async || !validID(j.ID) || seen[identifierKey(j.ID)] || j.AppID != b.AppID || j.DeploymentID != b.DeploymentID || !digestPattern.MatchString(j.PayloadSHA256) || len(j.Method) == 0 || len(j.Method) > 10 || len(j.Path) == 0 || len(j.Path) > 2048 || j.State != "completed" || len(j.Attempts) == 0 || len(j.Attempts) > MaxAttempts {
			return errors.New("managed job lacks complete execution identity")
		}
		seen[identifierKey(j.ID)] = true
		workers[j.Workload] = true
		for i, a := range j.Attempts {
			expected := "retry"
			if i == len(j.Attempts)-1 {
				expected = "succeeded"
			}
			if a.Number != i+1 || !validID(a.InstanceID) || a.DeploymentID != j.DeploymentID || a.Outcome != expected {
				return errors.New("managed attempt was lost, cancelled or unfinished")
			}
		}
	}
	for _, p := range e.Plan.Participants {
		if p.Async && !workers[p.Workload] {
			return errors.New("declared worker was not exercised")
		}
	}
	return e.validateWorkerFault()
}

func validID(id string) bool {
	return uuidPattern.MatchString(id) && identifierKey(id) != strings.Repeat("0", 32)
}

func identifierKey(id string) string { return strings.ReplaceAll(id, "-", "") }
