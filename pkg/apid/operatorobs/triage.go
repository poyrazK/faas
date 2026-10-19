package operatorobs

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func MutateTriage(ctx context.Context, store state.OperatorAlertTriageWriter, id, actor string, req api.ObsAlertTriageRequest, trace *string, now time.Time) (api.ObsAlertTriageResponse, error) {
	if req.ExpectedRevision == nil {
		return api.ObsAlertTriageResponse{}, state.ErrInvalidArgument
	}
	ctx, cancel := context.WithTimeout(ctx, api.ObsIncidentContextBudget)
	defer cancel()
	triage, err := store.MutateOperatorAlertTriage(ctx, state.OperatorAlertTriageMutation{EpisodeID: id, ActorID: actor, Action: req.Action, ExpectedRevision: *req.ExpectedRevision, SnoozedUntil: req.SnoozedUntil, Now: now, TraceID: trace})
	if err != nil {
		return api.ObsAlertTriageResponse{}, err
	}
	return api.ObsAlertTriageResponse{IncidentID: "platform_alert/" + id, Triage: triage.Projection(now)}, nil
}
