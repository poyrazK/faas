package managedpg

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// ResizeService is the managed PostgreSQL service's resize surface;
// *managedpostgres.Service satisfies it.
type ResizeService interface {
	Resize(ctx context.Context, request managedpostgres.ResizeDatabaseRequest) (managedpostgres.ResizeOperation, error)
	GetResize(ctx context.Context, account, database, id string) (managedpostgres.ResizeOperation, error)
}

// Resizes serves a customer's compute resizes: POST
// /v1/postgres/databases/{id}/resize journals one under the request's
// UUID, and GET .../resizes/{resize_id} reports it. Storage growth (ADR-1027)
// is a preview: outside Storage's accounts, storage_limit_bytes is an
// unknown field and the views carry no storage limits.
type Resizes struct {
	// Service is nil when managed PostgreSQL is not configured.
	Service ResizeService
	// Storage is who sees storage growth, read per request.
	Storage       func() Rollout
	Problem       func(http.ResponseWriter, error)
	NotConfigured func(http.ResponseWriter)
	Audit         interface {
		Emit(ctx context.Context, kind string, accountID *string, data map[string]any)
	}
}

// resizeBody is the request as sent, so an absent storage limit is
// distinguishable from zero.
type resizeBody struct {
	RequestID         string `json:"request_id"`
	ServiceClass      string `json:"service_class,omitempty"`
	StorageLimitBytes *int64 `json:"storage_limit_bytes,omitempty"`
}

// Post journals the resize and answers 202 with its view.
func (rs Resizes) Post(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if rs.Service == nil {
		rs.NotConfigured(w)
		return
	}
	storage := rs.Storage().Allows(acct.ID)
	var body resizeBody
	// Outside the preview a storage limit is an unknown field, as for any
	// field the API does not have.
	if err := httpkit.DecodeJSON(r, &body); err != nil || body.StorageLimitBytes != nil && !storage {
		rs.Problem(w, managedpostgres.ErrInvalid)
		return
	}
	request := api.ResizeManagedPostgresDatabaseRequest{RequestID: body.RequestID, ServiceClass: body.ServiceClass}
	if body.StorageLimitBytes != nil {
		request.StorageLimitBytes = *body.StorageLimitBytes
	}
	database := r.PathValue("id")
	if err := rs.admit(r.Context(), acct, database, request); err != nil {
		rs.Problem(w, err)
		return
	}
	operation, err := rs.Service.Resize(r.Context(), ResizeRequest(acct.ID, database, request))
	if err != nil {
		rs.Problem(w, err)
		return
	}
	view := resizeViewFor(operation, storage)
	if rs.Audit != nil {
		rs.Audit.Emit(r.Context(), "managed_postgres.database.resize_requested", &acct.ID, map[string]any{"database_id": database, "request_id": operation.ID,
			"target_class": view.TargetClass, "target_storage_limit_bytes": operation.TargetSpec().StorageLimitBytes, "generation": operation.Generation})
	}
	w.Header().Set("Location", "/v1/postgres/databases/"+url.PathEscape(database)+"/resizes/"+url.PathEscape(operation.ID))
	httpkit.WriteJSON(w, http.StatusAccepted, view)
}

// Get reports one of the database's resizes.
func (rs Resizes) Get(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if rs.Service == nil {
		rs.NotConfigured(w)
		return
	}
	operation, err := rs.Service.GetResize(r.Context(), acct.ID, r.PathValue("id"), r.PathValue("resize_id"))
	if err != nil {
		rs.Problem(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, resizeViewFor(operation, rs.Storage().Allows(acct.ID)))
}

// admit checks a new request against the plan. Accepted requests stay
// readable and replayable after a plan change.
func (rs Resizes) admit(ctx context.Context, acct state.Account, database string, request api.ResizeManagedPostgresDatabaseRequest) error {
	if _, err := rs.Service.GetResize(ctx, acct.ID, database, request.RequestID); err == nil {
		return nil
	} else if !errors.Is(err, managedpostgres.ErrNotFound) {
		return err
	}
	return AdmitResize(acct.Plan, request)
}

// resizeViewFor is ResizeView without storage limits outside the preview.
func resizeViewFor(operation managedpostgres.ResizeOperation, storage bool) api.ManagedPostgresResize {
	view := ResizeView(operation)
	if !storage {
		view.FromStorageLimitBytes, view.TargetStorageLimitBytes = 0, 0
	}
	return view
}
