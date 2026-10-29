package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// hiddenTriggerAgent refuses the agent field without the agents capability
// exactly as the decoder refuses any unknown field (ADR-20261027090010 §9).
func (s *server) hiddenTriggerAgent(acct state.Account, requested *string) *api.Problem {
	if requested == nil || s.agentStepsAllowed(acct) {
		return nil
	}
	return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", `json: unknown field "agent"`)
}

// requestedTriggerAgent validates a requested agent target; empty means the
// trigger invokes its app.
func (s *server) requestedTriggerAgent(ctx context.Context, acct state.Account, kind api.TriggerKind, delivery string, requested *string) (string, *api.Problem) {
	if requested == nil {
		return "", nil
	}
	if err := api.ValidateTriggerAgent(kind, delivery, *requested); err != nil {
		return "", api.NewProblem(http.StatusUnprocessableEntity, "trigger_invalid_config", "Invalid trigger agent", err.Error())
	}
	if s.agentAPI == nil || s.agentAPI.Store == nil {
		return "", api.ErrCapacity("the agent API is unavailable on this control-plane host")
	}
	_, _, err := s.agentAPI.Store.Agent(ctx, acct.ID, *requested)
	if errors.Is(err, agentstore.ErrNotFound) {
		return "", api.NewProblem(http.StatusUnprocessableEntity, "trigger_invalid_config", "Invalid trigger agent",
			fmt.Sprintf("agent: the account has no agent %q", *requested))
	}
	if err != nil {
		return "", api.ErrCapacity("could not read the trigger's agent")
	}
	return *requested, nil
}

// applyTriggerAgent points a just-created, disabled trigger at its agent and
// enables it when asked. A trigger that cannot be pointed is deleted again,
// so its records never reach the app instead.
func (s *server) applyTriggerAgent(ctx context.Context, t sqlc.Trigger, agent string, enable bool) (sqlc.Trigger, *api.Problem) {
	triggerID, appID := uuidFromPgtype(t.ID).String(), uuidFromPgtype(t.AppID).String()
	var pointed sqlc.Trigger
	err := errors.New("trigger agent store unavailable")
	if store, ok := s.store.(state.TriggerAgentStore); ok {
		pointed, err = store.SetTriggerAgent(ctx, triggerID, agent)
	}
	if err == nil && enable {
		pointed, err = s.store.UpdateTrigger(ctx, triggerID, &enable, nil, nil, nil, nil, nil, nil, nil, nil)
		if err == nil {
			_ = s.notif.Notify(ctx, db.NotifyTriggerChanged, notifyTriggerChangedJSON("updated", appID, triggerID))
		}
	}
	if err == nil {
		return pointed, nil
	}
	s.log.Warn("trigger agent target failed", "trigger", triggerID, "err", err)
	if delErr := s.store.DeleteTrigger(ctx, triggerID, appID); delErr != nil {
		s.log.Warn("trigger cleanup failed", "trigger", triggerID, "err", delErr)
	}
	_ = s.notif.Notify(ctx, db.NotifyTriggerChanged, notifyTriggerChangedJSON("deleted", appID, triggerID))
	return sqlc.Trigger{}, api.ErrCapacity("could not set the trigger's agent")
}
