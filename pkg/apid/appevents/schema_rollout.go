package appevents

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) PreviewEventSchemaRollout(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventSchemaRolloutTimeout)
	defer cancel()
	var req api.EventSchemaRolloutRequest
	if err := httpkit.DecodeJSONSized(r, &req, api.EventSchemaRolloutBodyMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid or oversized rollout preview body"))
		return
	}
	if err := req.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	store, ok := s.Host.Store().(state.EventSchemaRolloutStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("schema rollout preview"))
		return
	}
	out, err := store.PreviewEventSchemaRollout(ctx, acct.ID, req)
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation(err.Error()))
	case errors.Is(err, state.ErrEventSchemaUnknown):
		httpkit.NotFound(w, "schema version is not registered; provide a proposed schema")
	case errors.Is(err, context.DeadlineExceeded):
		api.WriteProblem(w, api.NewProblem(http.StatusGatewayTimeout, api.CodeInternal, "Schema rollout preview timed out", "retry with a narrower range"))
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("schema rollout preview"))
	default:
		if err := s.hideRolloutConsumers(ctx, acct, &out); err != nil {
			api.WriteProblem(w, api.ErrInternal("schema rollout preview"))
			return
		}
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}

// hideRolloutConsumers drops the consumers on apps the caller may not see,
// and their counts (ADR-20261025211616), as the event preview does.
func (s *Service) hideRolloutConsumers(ctx context.Context, acct state.Account, out *api.EventSchemaRolloutResponse) error {
	access, err := s.eventAccess(ctx, acct)
	if err != nil {
		return err
	}
	kept := out.Consumers[:0]
	for _, consumer := range out.Consumers {
		visible, err := access.app(consumer.AppID)
		if err != nil {
			return err
		}
		if visible {
			kept = append(kept, consumer)
			continue
		}
		out.ConsumerCount--
		if consumer.AcceptsVersion {
			out.AcceptingCount--
		} else {
			out.ExcludingCount--
		}
	}
	out.Consumers = kept
	return nil
}
