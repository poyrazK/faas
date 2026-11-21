package rehearsalpg

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Resource is private platform state. It contains no login password or source SQL.
type Resource struct {
	ID, AccountID, RunID, AppID, OwnerID string
	DatabaseName, RoleName, Status       string
	ParticipantAccess                    string // projected from the enrolled participant, never persisted in the fixture
	BackendSHA256                        string
	Spec                                 Spec
	Initial                              []Snapshot
	Evidence                             *Evidence
	TaskParticipants                     []TaskParticipant `json:"-"` // loaded from enrollment at sealing
	ExpiresAt                            time.Time
}

func (r Resource) ValidReservation() bool {
	if r.Spec.Validate() != nil || !digest.MatchString(r.BackendSHA256) || len(r.RunID) != 32 || strings.Trim(r.RunID, "0123456789abcdef") != "" || !r.ExpiresAt.After(time.Now()) || r.ExpiresAt.After(time.Now().Add(15*time.Minute)) {
		return false
	}
	for _, id := range []string{r.ID, r.AccountID, r.AppID, r.OwnerID} {
		u, err := uuid.Parse(id)
		if err != nil || u == uuid.Nil {
			return false
		}
	}
	id, _ := uuid.Parse(r.ID)
	return r.DatabaseName == "grpg_"+strings.ReplaceAll(id.String(), "-", "") && r.RoleName == r.DatabaseName
}

// TaskParticipant identifies an enrolled workload's restricted database role.
type TaskParticipant struct {
	AppID  string
	Access string
}

type Catalog interface {
	ScenarioPostgresTaskParticipants(context.Context, Resource) ([]TaskParticipant, error)
	ReserveScenarioPostgres(context.Context, Resource) (Resource, error)
	GetScenarioPostgres(context.Context, string, string) (Resource, error)
	ScenarioPostgresForApp(context.Context, string) (Resource, error)
	TransitionScenarioPostgres(context.Context, Resource, string) (Resource, error)
	ExpiredScenarioPostgres(context.Context) ([]Resource, error)
}

// CloneResource prevents test stores or callers from mutating retained intent.
func CloneResource(r Resource) Resource {
	b, _ := json.Marshal(r)
	var out Resource
	_ = json.Unmarshal(b, &out)
	return out
}
