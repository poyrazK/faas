package rehearsalobj

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Attempt struct {
	AppID, Workload string
	ID              string
	DeploymentID    string
	Method, Key     string
	Pending         bool
	Mutation        *Mutation
}

// Resource never crosses the REST boundary. Object bodies and sealed credentials
// are retained only in this private namespace, with a fixed expiry.
type Resource struct {
	CallerWorkload              string `json:"-"`
	CallerPermission            string `json:"-"`
	CallerDeploymentID          string `json:"-"`
	ID, AccountID, RunID, AppID string
	Status                      string
	AccessKey                   string
	SecretSealed                []byte
	ExpiresAt                   time.Time
	Spec                        Spec
	Initial                     []Object
	Current                     []Data
	Attempts                    []Attempt
	Failed                      bool
	Evidence                    *Evidence
	CollectorID                 string
	CollectorClosed             bool
}
type Catalog interface {
	ReserveScenarioObjects(context.Context, Resource) (Resource, error)
	GetScenarioObjects(context.Context, string, string) (Resource, error)
	ScenarioObjectsForApp(context.Context, string) (Resource, error)
	UpdateScenarioObjects(context.Context, string, string, string, func(*Resource) error) (Resource, error)
	ExpiredScenarioObjects(context.Context) ([]Resource, error)
	ActiveScenarioObjects(context.Context, string) ([]Resource, error)
}

func Clone(r Resource) Resource {
	b, _ := json.Marshal(r)
	var out Resource
	_ = json.Unmarshal(b, &out)
	return out
}
func (r Resource) Receipt() Receipt {
	return Receipt{Version: Version, RunID: r.RunID, AppID: r.AppID, ResourceID: r.ID, FixtureSHA256: r.Spec.FixtureSHA256, ContractSHA256: r.Spec.ContractSHA256(), InitialSHA256: Hash(r.Initial), CollectorID: r.CollectorID}
}
func (r Resource) ValidReservation() bool {
	for _, id := range []string{r.ID, r.AppID, r.AccountID} {
		u, e := uuid.Parse(id)
		if e != nil || u == uuid.Nil {
			return false
		}
	}
	s, err := Snapshot(r.Current)
	return r.Status == "ready" && r.CollectorID == "" && !r.CollectorClosed && r.Spec.Validate() == nil && len(r.RunID) == 32 && strings.Trim(r.RunID, "0123456789abcdef") == "" && r.ExpiresAt.After(time.Now()) && !r.ExpiresAt.After(time.Now().Add(15*time.Minute)) && len(r.AccessKey) >= 16 && len(r.SecretSealed) > 0 && len(r.Attempts) == 0 && !r.Failed && r.Evidence == nil && err == nil && Hash(s) == r.Spec.FixtureSHA256 && Hash(s) == Hash(r.Initial)
}
func ValidateUpdate(old, next Resource) error {
	a, b := Clone(old), Clone(next)
	a.Status, b.Status = "", ""
	a.Current, b.Current = nil, nil
	a.Attempts, b.Attempts = nil, nil
	a.Failed, b.Failed = false, false
	a.Evidence, b.Evidence = nil, nil
	a.CollectorID, b.CollectorID = "", ""
	a.CollectorClosed, b.CollectorClosed = false, false
	if old.CollectorID != "" && old.CollectorID != next.CollectorID || old.CollectorClosed && !next.CollectorClosed {
		return errors.New("object collector identity is immutable")
	}
	if old.CollectorID == "" && next.CollectorID != "" {
		id, e := uuid.Parse(next.CollectorID)
		if e != nil || id == uuid.Nil || old.Status != "ready" || next.Status != "ready" {
			return errors.New("invalid object collector claim")
		}
	}
	if !old.CollectorClosed && next.CollectorClosed {
		if old.Status != "sealing" || next.Status != "sealing" || old.CollectorID == "" {
			return errors.New("object collector cannot close")
		}
		for _, a := range next.Attempts {
			if a.Pending {
				return errors.New("object collector has pending requests")
			}
		}
	}
	if Hash(a) != Hash(b) || old.Failed && !next.Failed || len(next.Attempts) < len(old.Attempts) || len(next.Attempts) > MaxRequests {
		return errors.New("object journal intent is immutable")
	}
	if old.Status == "complete" || old.Status == "deleted" {
		if Hash(old) != Hash(next) {
			return errors.New("object journal is terminal")
		}
		return nil
	}
	validTransition := next.Status == old.Status || old.Status == "ready" && next.Status == "sealing" || old.Status == "sealing" && next.Status == "complete" || next.Status == "deleted"
	if !validTransition {
		return errors.New("invalid object journal transition")
	}
	if old.Status != "ready" && len(next.Attempts) != len(old.Attempts) {
		return errors.New("object admission is closed")
	}
	if _, err := Snapshot(next.Current); err != nil {
		return err
	}
	for i, attempt := range old.Attempts {
		n := next.Attempts[i]
		if attempt.ID != n.ID || attempt.AppID != n.AppID || attempt.Workload != n.Workload || attempt.DeploymentID != n.DeploymentID || attempt.Key != n.Key || attempt.Method != n.Method || !attempt.Pending && Hash(attempt) != Hash(n) {
			return errors.New("object attempt is immutable")
		}
	}
	if next.Status == "complete" && (next.Evidence == nil || next.Evidence.Validate() != nil || len(next.Current) != 0) {
		return errors.New("object cleanup and evidence are required")
	}
	if next.Status == "deleted" && len(next.Current) != 0 {
		return errors.New("object cleanup is required")
	}
	return nil
}
