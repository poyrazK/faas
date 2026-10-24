package state

import (
	"context"
	"encoding/json"
	"time"
)

// LogProjectionStore reads the source rows apid projects into the log_events
// ledger as deploy and build events (ADR-20261018083536 slice C). Both reads
// page forward by a (time, id) keyset and stop before an upper bound, so the
// projector can trail commit order by a short lag and never skip a row a
// slower transaction commits behind the cursor.
type LogProjectionStore interface {
	// ListDeploymentAuditForLedger returns deployment_audit rows of live
	// apps after (afterAt, afterID) and before until, oldest first.
	ListDeploymentAuditForLedger(ctx context.Context, afterAt time.Time, afterID int64, until time.Time, limit int) ([]LedgerDeploymentAudit, error)
	// ListBuildOutcomesForLedger returns finished builds of live apps after
	// (afterAt, afterID) by finished_at and before until, oldest first.
	ListBuildOutcomesForLedger(ctx context.Context, afterAt time.Time, afterID string, until time.Time, limit int) ([]LedgerBuildOutcome, error)
}

// LedgerDeploymentAudit is one deployment_audit row with its app's tenant.
type LedgerDeploymentAudit struct {
	ID           int64
	At           time.Time
	AccountID    string
	AppID        string
	DeploymentID string
	Kind         DeploymentAuditKind
	Actor        string
	Data         json.RawMessage
}

// LedgerBuildOutcome is one finished build with its app's tenant and the
// deployment's recorded error, if any.
type LedgerBuildOutcome struct {
	BuildID         string
	AccountID       string
	AppID           string
	DeploymentID    string
	Kind            string
	Status          BuildStatus
	FailureClass    FailureClass
	StartedAt       time.Time
	FinishedAt      time.Time
	DeploymentError string
}

const logProjectionPageMax = 1000

func logProjectionLimit(limit int) int {
	if limit < 1 || limit > logProjectionPageMax {
		return logProjectionPageMax
	}
	return limit
}

// afterKeyset reports whether (at, id) sorts after (afterAt, afterID).
func afterKeyset[T int64 | string](at time.Time, id T, afterAt time.Time, afterID T) bool {
	return at.After(afterAt) || (at.Equal(afterAt) && id > afterID)
}
