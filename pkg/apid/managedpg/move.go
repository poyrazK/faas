package managedpg

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// Move is the operator's move of a native managed database to another
// database node (ADR-1027): POST /v1/admin/ops/databases/{id}/move. Like
// the compute-node lifecycle operations it only records a durable
// database_move intent; schedd's database scheduler validates it against
// the database and the nodes, parks the database, records the move and
// copies it in the background. The intent succeeds once the move is
// recorded.
type Move struct {
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

// IntentWriter records operator intents (state.Store).
type IntentWriter interface {
	InsertOperatorIntent(ctx context.Context, kind state.OperatorIntentKind, targetID string, accountID *string,
		actorID, reason string, metadata json.RawMessage, traceID *string) (string, error)
}

// operatorIntentPollHorizon is how long the status URL is worth polling,
// as for apid's other operator intents.
const operatorIntentPollHorizon = 5 * time.Minute

var (
	moveReasonShape = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
	// moveNodeShape is a manifest host name, the database node's id.
	moveNodeShape = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,61}[a-z0-9])?$`)
)

// Post records the move. ?confirm=true is required, ?node= names the
// target (else schedd picks one), and ?reason= defaults to
// operator_database_move.
func (m Move) Post(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := m.AdminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	databaseID, node, reason, prob := parseMove(r)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	metadata, err := json.Marshal(state.DatabaseMoveIntentMetadata{TargetNode: node})
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal, "encode operator intent failed", err.Error()))
		return
	}
	intentID, err := m.Intents.InsertOperatorIntent(r.Context(), state.OperatorIntentKindDatabaseMove, databaseID, nil, acct.ID, reason, metadata, middleware.TraceIDFrom(r))
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal, "insert operator intent failed", err.Error()))
		return
	}
	payload, _ := json.Marshal(map[string]string{"intent_id": intentID, "kind": string(state.OperatorIntentKindDatabaseMove), "target_id": databaseID})
	// The 30 s safety tick claims the intent if this notification is lost.
	_ = m.Notifier.Notify(r.Context(), db.NotifyOperatorIntent, string(payload))
	if m.Audit != nil {
		subject := databaseID
		m.Audit.Emit(r.Context(), "operator.action."+string(state.OperatorIntentKindDatabaseMove), &subject, map[string]any{
			"actor": acct.ID, "intent_id": intentID, "database_id": databaseID, "target_node": node, "reason": reason, "result": "enqueued",
		})
	}
	httpkit.WriteJSON(w, http.StatusAccepted, api.ObsDatabaseMoveResponse{
		OK: true, IntentID: intentID, StatusURL: "/v1/admin/operator-intents/" + intentID,
		ExpiresAt: time.Now().UTC().Add(operatorIntentPollHorizon), Kind: string(state.OperatorIntentKindDatabaseMove),
		DatabaseID: databaseID, TargetNode: node, Reason: reason,
	})
}

func parseMove(r *http.Request) (databaseID, node, reason string, prob *api.Problem) {
	q := r.URL.Query()
	if q.Get("confirm") != "true" {
		return "", "", "", api.NewProblem(http.StatusBadRequest, api.CodeValidation, "confirm required", "?confirm=true is required to move a database")
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return "", "", "", api.NewProblem(http.StatusBadRequest, api.CodeValidation, "invalid database id", "the database id is the native database's UUID")
	}
	node = q.Get("node")
	if node != "" && !moveNodeShape.MatchString(node) {
		return "", "", "", api.NewProblem(http.StatusBadRequest, api.CodeValidation, "invalid node", "node must be a database node's id")
	}
	reason = q.Get("reason")
	if reason == "" {
		reason = "operator_database_move"
	}
	if !moveReasonShape.MatchString(reason) {
		return "", "", "", api.NewProblem(http.StatusBadRequest, api.CodeValidation, "invalid reason", "reason must match [a-z0-9_]{1,64}")
	}
	return id.String(), node, reason, nil
}
