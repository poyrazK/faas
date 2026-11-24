package managedpg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// SuspendTimeoutService is the managed PostgreSQL service's suspend timeout
// surface; *managedpostgres.Service satisfies it.
type SuspendTimeoutService interface {
	SetSuspendTimeout(ctx context.Context, accountID, databaseID string, seconds *int64) (managedpostgres.SuspendTimeout, error)
}

// SuspendTimeouts serves PUT /v1/postgres/databases/{id}/suspend-timeout:
// how long a scale-to-zero database stays up after its last activity, or
// null for the platform default.
type SuspendTimeouts struct {
	// Service is nil when managed PostgreSQL is not configured.
	Service       SuspendTimeoutService
	Problem       func(http.ResponseWriter, error)
	NotConfigured func(http.ResponseWriter)
	Audit         interface {
		Emit(ctx context.Context, kind string, accountID *string, data map[string]any)
	}
}

// suspendTimeoutBody keeps seconds raw so a missing field, which would
// silently reset the database to the default, is told apart from null.
type suspendTimeoutBody struct {
	Seconds json.RawMessage `json:"seconds"`
}

// Put sets the database's own suspend timeout and answers with the value in
// force and whether the compute already runs with it.
func (st SuspendTimeouts) Put(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if st.Service == nil {
		st.NotConfigured(w)
		return
	}
	seconds, problem := decodeSuspendTimeout(r)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if limits, ok := api.ManagedPostgresLimitsFor(acct.Plan); !ok || limits.DatabasesMax == 0 {
		st.Problem(w, managedpostgres.ErrNotInPlan)
		return
	}
	timeout, err := st.Service.SetSuspendTimeout(r.Context(), acct.ID, r.PathValue("id"), seconds)
	if errors.Is(err, managedpostgres.ErrSuspendTimeoutAlwaysOn) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "suspend_timeout_always_on", "Database is always-on",
			"An always-on database never suspends, so it has no suspend timeout. Turn scale-to-zero on with compute-policy first."))
		return
	}
	if err != nil {
		st.Problem(w, err)
		return
	}
	if st.Audit != nil {
		st.Audit.Emit(r.Context(), "managed_postgres.database.suspend_timeout_set", &acct.ID,
			map[string]any{"database_id": timeout.DatabaseID, "seconds": timeout.Seconds, "custom": timeout.Custom})
	}
	httpkit.WriteJSON(w, http.StatusOK, api.ManagedPostgresSuspendTimeout{DatabaseID: timeout.DatabaseID,
		Seconds: timeout.Seconds, Custom: timeout.Custom, Applied: timeout.Applied})
}

// decodeSuspendTimeout reads the request's seconds: a number in the
// customer's range, or null for the default. A missing field is refused.
func decodeSuspendTimeout(r *http.Request) (*int64, *api.Problem) {
	invalid := func(detail string) *api.Problem {
		return api.NewProblem(http.StatusUnprocessableEntity, "invalid_suspend_timeout", "Invalid suspend timeout", detail).
			WithDocs(api.ManagedPostgresSuspendTimeoutDocsURL)
	}
	var body suspendTimeoutBody
	if err := httpkit.DecodeJSON(r, &body); err != nil {
		return nil, invalid("the body must be {\"seconds\": <seconds>} or {\"seconds\": null}")
	}
	if len(body.Seconds) == 0 {
		return nil, invalid("seconds is required: a number of seconds, or null for the platform default")
	}
	var seconds *int64
	if err := json.Unmarshal(body.Seconds, &seconds); err != nil {
		return nil, invalid("seconds must be a whole number of seconds, or null for the platform default")
	}
	if seconds != nil && !managedpostgres.ValidCustomerSuspendTimeout(*seconds) {
		limit := int64(api.ManagedPostgresSuspendTimeoutMaxSeconds)
		if *seconds < api.ManagedPostgresSuspendTimeoutMinSeconds {
			limit = api.ManagedPostgresSuspendTimeoutMinSeconds
		}
		return nil, invalid(fmt.Sprintf("seconds must be between %d and %d, or null for the default; got %d",
			api.ManagedPostgresSuspendTimeoutMinSeconds, api.ManagedPostgresSuspendTimeoutMaxSeconds, *seconds)).
			WithLimit(limit, *seconds)
	}
	return seconds, nil
}

// ApplySuspendTimeout adds a scale-to-zero database's suspend timeout to its
// view. The value is the one the database is set to; a running compute takes
// a new value the next time it suspends.
func ApplySuspendTimeout(view *api.ManagedPostgresDatabase, timeouts map[string]managedpostgres.SuspendTimeout) {
	if timeout, ok := timeouts[view.ID]; ok && view.ScaleToZero {
		view.SuspendTimeoutSeconds, view.SuspendTimeoutCustom = timeout.Seconds, timeout.Custom
	}
}
