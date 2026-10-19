package managedrealtime

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) ManageRealtimeAccess(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.previewUnavailable(w, acct.ID, s.AccessPreviewEnabled, "realtime access preview unavailable") {
		return
	}
	ep, _, ok := s.loadManagedRealtimeEndpoint(w, r, acct)
	if !ok {
		return
	}
	if ep.AuthMode != "oidc_jwt" && !s.TicketsPreviewEnabled {
		api.WriteProblem(w, api.ErrRealtimeInvalid("access controls require OIDC authentication"))
		return
	}
	store, ok := s.Host.Store().(state.ManagedRealtimeAccessStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("realtime access store unavailable"))
		return
	}
	var result state.ManagedRealtimeAccess
	var err error
	principal := r.PathValue("principal")
	if r.Method == http.MethodGet {
		result, err = store.ReadManagedRealtimeAccess(r.Context(), ep.ID, principal)
	} else {
		var request api.ManagedRealtimeAccessRequest
		if decodeErr := httpkit.DecodeJSONSized(r, &request, 4096); decodeErr != nil {
			api.WriteProblem(w, api.ErrRealtimeInvalid("invalid access request"))
			return
		}
		result, err = store.ChangeManagedRealtimeAccess(r.Context(), ep.ID, principal, request.Action, request.Channel)
		if err == nil {
			s.Host.EmitAudit(r.Context(), "realtime.access_changed", &acct.ID, map[string]any{"endpoint_id": ep.ID, "action": request.Action, "revision": result.Revision})
		}
	}
	s.writeRealtimeAccessResult(w, result, err)
}

func (s *Service) writeRealtimeAccessResult(w http.ResponseWriter, result state.ManagedRealtimeAccess, err error) {
	var limit *state.ManagedRealtimeAccessLimitError
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid principal, action or channel"))
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "realtime endpoint unavailable")
	case errors.As(err, &limit):
		api.WriteProblem(w, api.ErrRealtimeInvalid("realtime access preview capacity reached").WithLimit(int64(limit.Limit), int64(limit.Observed)).WithDocs("https://gregale.dev/docs/realtime#principal-access-controls-preview"))
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("realtime access unavailable"))
	default:
		w.Header().Set("Cache-Control", "no-store")
		httpkit.WriteJSON(w, http.StatusOK, api.ManagedRealtimeAccessResponse{
			DeniedChannels: result.DeniedChannels, SessionsRevokedBefore: result.SessionsRevokedBefore, Revision: result.Revision, UpdatedAt: result.UpdatedAt,
		})
	}
}
