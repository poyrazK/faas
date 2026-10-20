package managedpg

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// Restart is the operator's in-place restart of a native managed database
// (ADR-1027): POST /v1/admin/ops/databases/{id}/restart. It records a
// durable database_restart intent; schedd's database scheduler parks the
// running database and wakes it again on its node's current base (a new
// PostgreSQL minor, guest-init or launch), standby included. A parked
// database is left for its next wake.
type Restart struct {
	AdminAllows func(state.Account) (bool, *api.Problem)
	Intents     IntentWriter
	Notifier    interface {
		Notify(ctx context.Context, channel, payload string) error
	}
	// Audit is optional.
	Audit interface {
		Emit(ctx context.Context, kind string, accountID *string, data map[string]any)
	}
}

// Post records the restart. ?confirm=true is required and ?reason=
// defaults to operator_database_restart.
func (rs Restart) Post(w http.ResponseWriter, r *http.Request, acct state.Account) {
	databaseID, reason, ok := acceptOperation(w, r, acct, rs.AdminAllows, "restart", "operator_database_restart")
	if !ok {
		return
	}
	intentID, ok := enqueue(w, r, rs.Intents, rs.Notifier, rs.Audit, acct, state.OperatorIntentKindDatabaseRestart, databaseID, reason, nil, nil)
	if !ok {
		return
	}
	httpkit.WriteJSON(w, http.StatusAccepted, api.ObsDatabaseRestartResponse{
		OK: true, IntentID: intentID, StatusURL: "/v1/admin/operator-intents/" + intentID,
		ExpiresAt: time.Now().UTC().Add(operatorIntentPollHorizon), Kind: string(state.OperatorIntentKindDatabaseRestart),
		DatabaseID: databaseID, Reason: reason,
	})
}

// acceptOperation checks an operator's request for one database
// operation (verb): an operator, ?confirm=true, a database UUID and a
// reason (defaultReason when empty). It answers the problem itself and
// reports false when the request is refused.
func acceptOperation(w http.ResponseWriter, r *http.Request, acct state.Account, adminAllows func(state.Account) (bool, *api.Problem),
	verb, defaultReason string) (string, string, bool) {
	if allowed, prob := adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return "", "", false
	}
	q := r.URL.Query()
	if q.Get("confirm") != "true" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "confirm required", "?confirm=true is required to "+verb+" a database"))
		return "", "", false
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "invalid database id", "the database id is the native database's UUID"))
		return "", "", false
	}
	reason := q.Get("reason")
	if reason == "" {
		reason = defaultReason
	}
	if !moveReasonShape.MatchString(reason) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "invalid reason", "reason must match [a-z0-9_]{1,64}"))
		return "", "", false
	}
	return id.String(), reason, true
}

// Operations are the operator's native database operations (ADR-1027).
type Operations struct {
	AdminAllows func(state.Account) (bool, *api.Problem)
	Intents     IntentWriter
	Notifier    interface {
		Notify(ctx context.Context, channel, payload string) error
	}
	Audit interface {
		Emit(ctx context.Context, kind string, accountID *string, data map[string]any)
	}
}

// Register serves the move, the restart and the switchover on mux behind
// apid's trace, authentication and admin-mutation middleware.
func (o Operations) Register(mux *http.ServeMux, trace func(http.Handler) http.Handler,
	auth func(httpkit.AccountHandler) http.HandlerFunc, mutation func(httpkit.AccountHandler) httpkit.AccountHandler) {
	move, restart, switchover := Move(o), Restart(o), Switchover(o)
	mux.Handle("POST /v1/admin/ops/databases/{id}/move", trace(auth(mutation(move.Post))))
	mux.Handle("POST /v1/admin/ops/databases/{id}/restart", trace(auth(mutation(restart.Post))))
	mux.Handle("POST /v1/admin/ops/databases/{id}/switchover", trace(auth(mutation(switchover.Post))))
}
