// Package managedpg holds apid's managed PostgreSQL request admission and
// views that need no server state (ADR-982): the plan checks a resize
// request passes before the service journals it, and the resize view.
package managedpg

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// StorageDocsURL documents storage limits and their growth.
const StorageDocsURL = "https://gregale.dev/docs/managed-postgres#storage"

// StorageLimitError is a storage target above the plan's per-database
// limit. It is a managedpostgres.ErrQuotaExceeded.
type StorageLimitError struct {
	Limit, Observed int64
}

func (e *StorageLimitError) Error() string {
	return fmt.Sprintf("storage limit %d bytes exceeds the plan's %d bytes per database", e.Observed, e.Limit)
}

func (e *StorageLimitError) Unwrap() error { return managedpostgres.ErrQuotaExceeded }

// LimitProblem renders a StorageLimitError with its limit, observed value
// and docs; nil for any other error.
func LimitProblem(err error) *api.Problem {
	var limit *StorageLimitError
	if !errors.As(err, &limit) {
		return nil
	}
	return api.NewProblem(http.StatusForbidden, "managed_postgres_quota_exceeded", "Managed PostgreSQL quota exceeded", limit.Error()).
		WithByteLimit(limit.Limit, limit.Observed).WithDocs(StorageDocsURL)
}

// PlanAllowsClass reports whether the plan includes the service class.
func PlanAllowsClass(limits api.ManagedPostgresPlanLimits, class managedpostgres.ServiceClass) bool {
	switch class {
	case managedpostgres.ClassDevelopment:
		return limits.DevelopmentAllowed
	case managedpostgres.ClassBurstable:
		return limits.BurstableAllowed
	case managedpostgres.ClassProduction:
		return limits.ProductionAllowed
	default:
		return false
	}
}

// AdmitResize checks a new resize request against the plan: a class, if
// named, must be one the plan includes, and a storage limit, if named, must
// fit the plan's per-database limit. Whether storage grows is the
// service's to check, against the database.
func AdmitResize(plan api.Plan, request api.ResizeManagedPostgresDatabaseRequest) error {
	class := managedpostgres.ServiceClass(request.ServiceClass)
	if class == "" && request.StorageLimitBytes == 0 || request.StorageLimitBytes < 0 {
		return managedpostgres.ErrInvalid
	}
	switch class {
	case "", managedpostgres.ClassDevelopment, managedpostgres.ClassBurstable, managedpostgres.ClassProduction:
	default:
		return managedpostgres.ErrInvalid
	}
	limits, ok := api.ManagedPostgresLimitsFor(plan)
	if !ok || limits.DatabasesMax == 0 || class != "" && !PlanAllowsClass(limits, class) {
		return managedpostgres.ErrNotInPlan
	}
	if request.StorageLimitBytes > limits.StorageLimitBytes {
		return &StorageLimitError{Limit: limits.StorageLimitBytes, Observed: request.StorageLimitBytes}
	}
	return nil
}

// ResizeRequest is the service request for an admitted API request.
func ResizeRequest(accountID, databaseID string, request api.ResizeManagedPostgresDatabaseRequest) managedpostgres.ResizeDatabaseRequest {
	return managedpostgres.ResizeDatabaseRequest{AccountID: accountID, DatabaseID: databaseID, RequestID: request.RequestID,
		TargetClass: managedpostgres.ServiceClass(request.ServiceClass), TargetStorageLimitBytes: request.StorageLimitBytes}
}

// ResizeView exposes a resize's logical identity and observed progress.
func ResizeView(operation managedpostgres.ResizeOperation) api.ManagedPostgresResize {
	target := operation.TargetSpec()
	out := api.ManagedPostgresResize{ID: operation.ID, DatabaseID: operation.DatabaseID, FromClass: string(operation.SourceSpec.Class),
		TargetClass: string(target.Class), FromStorageLimitBytes: operation.SourceSpec.StorageLimitBytes, TargetStorageLimitBytes: target.StorageLimitBytes,
		Generation: operation.Generation, State: string(operation.State), ConnectionInterruptionExpected: true,
		LastErrorCode: operation.LastErrorCode, CreatedAt: operation.CreatedAt.UTC().Format(time.RFC3339Nano)}
	if !operation.CompletedAt.IsZero() {
		out.CompletedAt = operation.CompletedAt.UTC().Format(time.RFC3339Nano)
	}
	return out
}
