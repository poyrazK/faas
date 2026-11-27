package dbqueryerrors

import (
	"regexp"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
)

const (
	Retention          = 24 * time.Hour
	MaxRetained        = 1000
	MaxBatch           = 128
	MaxDatabaseIDs     = 2048
	DefaultResultLimit = 40
	MaxResultLimit     = 100
)

var (
	sqlState = regexp.MustCompile(`^[A-Z0-9]{5}$`)
	roleName = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)
)

func ValidID(s string) bool {
	id, err := uuid.Parse(s)
	return err == nil && id != uuid.Nil && id.String() == s
}

func ValidSQLState(code string) bool { return sqlState.MatchString(code) }

func ValidSeverity(severity string) bool {
	return severity == "ERROR" || severity == "FATAL" || severity == "PANIC"
}

type Event struct {
	ID         string    `json:"id"`
	RuntimeID  string    `json:"runtime_id"`
	Epoch      string    `json:"epoch"`
	ObservedAt time.Time `json:"observed_at"`
	Severity   string    `json:"severity"`
	SQLState   string    `json:"sqlstate"`
	RoleName   string    `json:"role_name"`
}

func (e Event) Valid() bool {
	return ValidID(e.ID) && ValidID(e.RuntimeID) && ValidID(e.Epoch) && !e.ObservedAt.IsZero() &&
		ValidSeverity(e.Severity) && ValidSQLState(e.SQLState) && roleName.MatchString(e.RoleName)
}

func (e Event) Proto() *scheddpb.DatabasePooledError {
	return &scheddpb.DatabasePooledError{
		Id: e.ID, RuntimeDatabaseId: e.RuntimeID, PolicyEpoch: e.Epoch, ObservedAt: timestamppb.New(e.ObservedAt),
		Severity: e.Severity, Sqlstate: e.SQLState, RoleName: e.RoleName,
	}
}

func Decode(p *scheddpb.DatabasePooledError) Event {
	e := Event{
		ID: p.GetId(), RuntimeID: p.GetRuntimeDatabaseId(), Epoch: p.GetPolicyEpoch(),
		Severity: p.GetSeverity(), SQLState: p.GetSqlstate(), RoleName: p.GetRoleName(),
	}
	if at := p.GetObservedAt(); at.CheckValid() == nil {
		e.ObservedAt = at.AsTime()
	}
	return e
}

type Policy struct {
	RuntimeID string
	Epoch     string
}
