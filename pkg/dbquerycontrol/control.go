package dbquerycontrol

import (
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
)

const (
	MaxTargets  = 512
	MaxCommands = 4
	SnapshotTTL = 3 * time.Second
	CommandTTL  = 6 * time.Second
	SignalSent  = "signal_sent"
	Stale       = "stale_target"
	Unconfirmed = "unconfirmed"
)

type Command struct {
	Target    Target
	ExpiresAt time.Time
}

type Target struct {
	ID                 string `json:"id"`
	DatabaseID         string `json:"database_id"`
	NodeID             string `json:"node_id"`
	PrimaryEpochMicros int64  `json:"primary_epoch_micros"`
	BackendPID         int32  `json:"backend_pid"`
	ApplicationName    string `json:"application_name,omitempty"`
}

func ValidID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func (t Target) Valid() bool {
	return ValidID(t.ID) && ValidID(t.DatabaseID) && t.NodeID != "" && len(t.NodeID) <= 128 &&
		t.PrimaryEpochMicros > 0 && t.BackendPID > 0 && len(t.ApplicationName) <= 128 && utf8.ValidString(t.ApplicationName)
}

func (t Target) Proto() *scheddpb.DatabaseQueryTarget {
	return &scheddpb.DatabaseQueryTarget{
		TargetId: t.ID, DatabaseId: t.DatabaseID, NodeId: t.NodeID, PrimaryEpochMicros: t.PrimaryEpochMicros,
		BackendPid: t.BackendPID, ApplicationName: t.ApplicationName,
	}
}

func FromProto(t *scheddpb.DatabaseQueryTarget) Target {
	return Target{
		ID: t.GetTargetId(), DatabaseID: t.GetDatabaseId(), NodeID: t.GetNodeId(),
		PrimaryEpochMicros: t.GetPrimaryEpochMicros(), BackendPID: t.GetBackendPid(), ApplicationName: t.GetApplicationName(),
	}
}

func ValidOutcome(outcome string) bool {
	return outcome == SignalSent || outcome == Stale || outcome == Unconfirmed
}
