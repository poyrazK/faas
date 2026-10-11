package state

import (
	"context"
	"time"
)

// This is a private resource ownership receipt. Ready is only maintenance
// readiness, never source drainage or a common database/object data point.
// Its UUID is private and stable per exact dataset, independent of a public
// operation UUID or a worker lease. It contains no connection credentials.
type ProjectEnvironmentClonePostgresMaintenance struct {
	ID, SourceDatabaseID, ReservedByOperationID    string
	BackendID, BackendFingerprint                  string
	SourceProviderResourceID, SourceDataResourceID string
	State                                          string
	OwnerOID, DatabaseOID                          uint32
	RoleRequestedAt, DatabaseRequestedAt           time.Time
	ActivationRequestedAt, ReadyAt, CreatedAt      time.Time
}

// The trusted adapter must independently authenticate this exact private
// owner/dataset and SQL identity before recording its observation.
type ProjectEnvironmentClonePostgresMaintenanceObservation struct {
	OwnerToken, SourceDataResourceID, State string
	OwnerOID, DatabaseOID                   uint32
}

type ProjectEnvironmentClonePostgresMaintenanceStore interface {
	ReserveProjectEnvironmentClonePostgresMaintenance(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentClonePostgresMaintenance, error)
	ProjectEnvironmentClonePostgresMaintenanceForLease(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentClonePostgresMaintenance, error)
	ClaimProjectEnvironmentClonePostgresMaintenanceDispatch(context.Context, ProjectEnvironmentCloneLease, string, string) (ProjectEnvironmentClonePostgresMaintenance, bool, error)
	RecordProjectEnvironmentClonePostgresMaintenance(context.Context, ProjectEnvironmentCloneLease, string, string, ProjectEnvironmentClonePostgresMaintenanceObservation) (ProjectEnvironmentClonePostgresMaintenance, error)
}

func postgresMaintenancePhase(phase string) (before, requested, complete string) {
	switch phase {
	case "role":
		return "reserved", "role_requested", "role_reserved"
	case "database":
		return "role_reserved", "database_requested", "database_created"
	case "activation":
		return "database_created", "activation_requested", "ready"
	default:
		return "", "", ""
	}
}

func postgresMaintenanceStateOrder(value string) int {
	for i, state := range []string{"reserved", "role_requested", "role_reserved", "database_requested", "database_created", "activation_requested", "ready"} {
		if value == state {
			return i
		}
	}
	return -1
}
