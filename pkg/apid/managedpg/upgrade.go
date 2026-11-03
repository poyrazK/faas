package managedpg

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// UpgradeService is the managed PostgreSQL service's major-upgrade surface;
// *managedpostgres.Service satisfies it.
type UpgradeService interface {
	UpgradeMajor(ctx context.Context, request managedpostgres.UpgradeMajorRequest) (managedpostgres.ResizeOperation, error)
	GetMajorUpgrade(ctx context.Context, account, database, id string) (managedpostgres.ResizeOperation, error)
}

// Upgrades serves a customer's in-place major upgrades of a managed
// database (ADR-1027): POST /v1/postgres/databases/{id}/upgrade journals
// one under the request's UUID, and GET .../upgrades/{upgrade_id} reports
// it. Every plan may upgrade; the backend must offer the major. The
// database restarts on the new major, so clients reconnect once.
type Upgrades struct {
	// Service is nil when managed PostgreSQL is not configured.
	Service UpgradeService
	// Problem renders a managed PostgreSQL error; NotConfigured the
	// unconfigured service.
	Problem       func(http.ResponseWriter, error)
	NotConfigured func(http.ResponseWriter)
	Audit         interface {
		Emit(ctx context.Context, kind string, accountID *string, data map[string]any)
	}
}

// Post journals the upgrade and answers 202 with its view.
func (u Upgrades) Post(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if u.Service == nil {
		u.NotConfigured(w)
		return
	}
	var request api.UpgradeManagedPostgresDatabaseRequest
	if err := httpkit.DecodeJSON(r, &request); err != nil || request.PostgresMajor <= 0 {
		u.Problem(w, managedpostgres.ErrInvalid)
		return
	}
	database := r.PathValue("id")
	operation, err := u.Service.UpgradeMajor(r.Context(), managedpostgres.UpgradeMajorRequest{AccountID: acct.ID, DatabaseID: database,
		RequestID: request.RequestID, PostgresMajor: request.PostgresMajor})
	if err != nil {
		u.Problem(w, err)
		return
	}
	if u.Audit != nil {
		u.Audit.Emit(r.Context(), "managed_postgres.database.upgrade_requested", &acct.ID, map[string]any{"database_id": database,
			"request_id": operation.ID, "from_postgres_major": operation.SourceSpec.PostgresMajor, "postgres_major": operation.TargetPostgresMajor,
			"generation": operation.Generation})
	}
	w.Header().Set("Location", "/v1/postgres/databases/"+url.PathEscape(database)+"/upgrades/"+url.PathEscape(operation.ID))
	httpkit.WriteJSON(w, http.StatusAccepted, UpgradeView(operation))
}

// Get reports one of the database's upgrades.
func (u Upgrades) Get(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if u.Service == nil {
		u.NotConfigured(w)
		return
	}
	operation, err := u.Service.GetMajorUpgrade(r.Context(), acct.ID, r.PathValue("id"), r.PathValue("upgrade_id"))
	if err != nil {
		u.Problem(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, UpgradeView(operation))
}

// UpgradeView exposes an upgrade's logical identity and observed progress;
// a failed one reports why in LastErrorCode.
func UpgradeView(operation managedpostgres.ResizeOperation) api.ManagedPostgresMajorUpgrade {
	out := api.ManagedPostgresMajorUpgrade{ID: operation.ID, DatabaseID: operation.DatabaseID, FromPostgresMajor: operation.SourceSpec.PostgresMajor,
		TargetPostgresMajor: operation.TargetPostgresMajor, Generation: operation.Generation, State: string(operation.State),
		ConnectionInterruptionExpected: true, LastErrorCode: operation.LastErrorCode, CreatedAt: operation.CreatedAt.UTC().Format(time.RFC3339Nano)}
	if operation.State == managedpostgres.ResizeFailed {
		out.LastErrorCode = operation.ErrorCode
	}
	if !operation.CompletedAt.IsZero() {
		out.CompletedAt = operation.CompletedAt.UTC().Format(time.RFC3339Nano)
	}
	return out
}
