package managedpg

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// Switchover is the operator's planned switchover of a production-class
// native database to its synchronized standby (ADR-1027):
// POST /v1/admin/ops/databases/{id}/switchover. It records a durable
// database_switchover intent; schedd's database scheduler stops a running
// primary cleanly and promotes its standby, or moves a parked database's
// record to its standby's node. A database without a synchronized
// standby on an active node fails the intent.
type Switchover struct {
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

// Post records the switchover. ?confirm=true is required and ?reason=
// defaults to operator_database_switchover.
func (sw Switchover) Post(w http.ResponseWriter, r *http.Request, acct state.Account) {
	databaseID, reason, ok := acceptOperation(w, r, acct, sw.AdminAllows, "switch over", "operator_database_switchover")
	if !ok {
		return
	}
	intentID, ok := enqueue(w, r, sw.Intents, sw.Notifier, sw.Audit, acct, state.OperatorIntentKindDatabaseSwitchover, databaseID, reason, nil, nil)
	if !ok {
		return
	}
	httpkit.WriteJSON(w, http.StatusAccepted, api.ObsDatabaseSwitchoverResponse{
		OK: true, IntentID: intentID, StatusURL: "/v1/admin/operator-intents/" + intentID,
		ExpiresAt: time.Now().UTC().Add(operatorIntentPollHorizon), Kind: string(state.OperatorIntentKindDatabaseSwitchover),
		DatabaseID: databaseID, Reason: reason,
	})
}
