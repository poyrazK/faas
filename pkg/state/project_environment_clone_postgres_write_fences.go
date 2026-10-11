package state

import (
	"context"
	"time"
)

// This private reservation precedes provider maintenance/closure IO. Held is
// recovery authority, not an acknowledgement that SQL admission is closed.
// Worker lease expiry never discards the hold. There is no successful capture
// release seam until retained sources and the common checkpoint are verified.
type ProjectEnvironmentClonePostgresWriteFence struct {
	OperationID, SourceDatabaseID, SourceVersion   string
	BackendID, BackendFingerprint                  string
	SourceProviderResourceID, SourceDataResourceID string
	State, RemoteTerminalState                     string
	RemoteReleasedAt, ReleasedAt, CreatedAt        time.Time
}

// Only an authenticated provider observation of the exact owned remote
// terminal record can release an abandoned capture's source hold. A missing
// remote row, client cancellation or lease expiry is not terminal evidence.
type ProjectEnvironmentClonePostgresFenceAbandonment struct {
	OwnerToken, SourceDataResourceID, State string
	ReleasedAt                              time.Time
}

type ProjectEnvironmentClonePostgresWriteFenceStore interface {
	ReserveProjectEnvironmentClonePostgresWriteFence(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentClonePostgresWriteFence, error)
	ProjectEnvironmentClonePostgresWriteFencesForLease(context.Context, ProjectEnvironmentCloneLease) ([]ProjectEnvironmentClonePostgresWriteFence, error)
	BeginProjectEnvironmentClonePostgresWriteFenceAbandonment(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentClonePostgresWriteFence, error)
	FinishProjectEnvironmentClonePostgresWriteFenceAbandonment(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentClonePostgresFenceAbandonment) (ProjectEnvironmentClonePostgresWriteFence, error)
}

func capturedCloneWriteFenceDatabase(views []ProjectEnvironmentCloneBindings, sourceID string) (ProjectEnvironmentClonePostgresBinding, error) {
	var out ProjectEnvironmentClonePostgresBinding
	for _, view := range views {
		for _, source := range view.Postgres {
			if source.DatabaseID != sourceID {
				continue
			}
			if !validClonePostgresBinding(source) || source.DataResourceID == "" || out.DatabaseID != "" && !sameClonePostgresDatabase(out, source) {
				return out, ErrProjectEnvironmentCloneBindingCapture
			}
			out = source
		}
	}
	if out.DatabaseID == "" {
		return out, ErrNotFound
	}
	return out, nil
}
