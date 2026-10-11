package main

import (
	"encoding/base64"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) mutateManagedRealtimeMessage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.realtimeHistoryPreviewEnabled {
		s.notFound(w, "retained message mutations unavailable")
		return
	}
	endpoint, _, ok := s.loadManagedRealtimeEndpoint(w, r, acct)
	if !ok {
		return
	}
	if !endpoint.Enabled {
		api.WriteProblem(w, api.ErrRealtimeInvalid("endpoint must be enabled"))
		return
	}
	var request api.ManagedRealtimeMessageMutationRequest
	if err := decodeJSON(r, &request); err != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid message mutation request"))
		return
	}
	data, err := base64.StdEncoding.DecodeString(request.DataBase64)
	remove := r.Method == http.MethodDelete
	if err != nil || len(data) > state.ManagedRealtimeHistoryMaxPayloadBytes || (remove && (request.DataBase64 != "" || request.Binary)) {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid mutation payload; deletes accept only expected_version"))
		return
	}
	if endpoint.MaxMessageBytes > 0 && int64(len(data)) > endpoint.MaxMessageBytes {
		api.WriteProblem(w, api.ErrRealtimeInvalid("mutation exceeds endpoint message limit"))
		return
	}
	stream := r.PathValue("channel")
	inbox := stream == ""
	if inbox {
		stream = r.URL.Query().Get("principal")
		if api.ValidateRealtimePrincipal(stream) != nil {
			api.WriteProblem(w, api.ErrRealtimeInvalid("invalid principal"))
			return
		}
	}
	store, ok := s.store.(state.ManagedRealtimeMutationStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("message mutation storage unavailable"))
		return
	}
	result, err := store.MutateManagedRealtimeMessage(r.Context(), endpoint.ID, stream, inbox, r.PathValue("message_id"), request.ExpectedVersion, data, request.Binary, remove)
	if err != nil {
		s.writeRealtimeInboxError(w, r, err)
		return
	}
	s.audit.Emit(r.Context(), "realtime.message_"+result.MessageEvent, &acct.ID, map[string]any{"endpoint_id": endpoint.ID, "message_id": result.TargetMessageID, "version": result.Version, "sequence": result.Sequence, "inbox": inbox})
	writeJSON(w, http.StatusOK, api.ManagedRealtimeMessageMutationResponse{MessageID: result.TargetMessageID, Version: result.Version, Sequence: result.Sequence, Event: result.MessageEvent, Deleted: result.Deleted})
}
