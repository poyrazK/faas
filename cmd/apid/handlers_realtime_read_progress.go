package main

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"time"
)

func (s *server) managedRealtimeReadProgress(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.realtimeHistoryPreviewEnabled {
		s.notFound(w, "read progress preview unavailable")
		return
	}
	endpoint, _, ok := s.loadManagedRealtimeEndpoint(w, r, acct)
	if !ok {
		return
	}
	principal := r.URL.Query().Get("principal")
	if api.ValidateRealtimePrincipal(principal) != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid principal"))
		return
	}
	channel := r.PathValue("channel")
	inbox := channel == ""
	store, ok := s.store.(state.ManagedRealtimeReadProgressStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("read progress unavailable"))
		return
	}
	var progress state.ManagedRealtimeReadProgress
	var err error
	if r.Method == http.MethodPost {
		if !endpoint.Enabled {
			api.WriteProblem(w, api.ErrRealtimeInvalid("endpoint must be enabled"))
			return
		}
		var request api.ManagedRealtimeReadProgressRequest
		if decodeJSON(r, &request) != nil {
			api.WriteProblem(w, api.ErrRealtimeInvalid("invalid read progress request"))
			return
		}
		progress, err = store.AdvanceManagedRealtimeReadProgress(r.Context(), endpoint.ID, principal, channel, inbox, request.Sequence)
		if err == nil {
			reader, _ := state.ManagedRealtimeReadPrincipalKey(principal)
			if router, ok := s.realtimeOwner.(realtimeEphemeralRouter); ok {
				route := channel
				if inbox {
					route = reader
				}
				relayCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
				_ = router.RelayEphemeral(relayCtx, "", endpoint.ID, route, realtime.EphemeralFrame{Type: "read_receipt", MemberID: reader, Inbox: inbox, Sequence: progress.Sequence, Unread: progress.Unread, LatestSequence: progress.LatestSequence, OldestSequence: progress.OldestSequence, HistoryUnavailable: progress.HistoryUnavailable, UpdatedAt: progress.UpdatedAt})
				cancel()
			}
			s.audit.Emit(r.Context(), "realtime.read_progress_marked", &acct.ID, map[string]any{"endpoint_id": endpoint.ID, "channel": channel, "inbox": inbox, "sequence": progress.Sequence})
		}
	} else {
		progress, err = store.GetManagedRealtimeReadProgress(r.Context(), endpoint.ID, principal, channel, inbox)
	}
	if err != nil {
		if errors.Is(err, state.ErrManagedRealtimeDurableCursorLimit) {
			api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, api.CodeCapacity, "Read marker limit reached", "the endpoint has reached its 1024 principal/stream read marker limit"))
			return
		}
		s.writeRealtimeInboxError(w, r, err)
		return
	}
	response := api.ManagedRealtimeReadProgressResponse{Sequence: progress.Sequence, Unread: progress.Unread, OldestSequence: progress.OldestSequence, LatestSequence: progress.LatestSequence, HistoryUnavailable: progress.HistoryUnavailable}
	if !progress.UpdatedAt.IsZero() {
		response.UpdatedAt = progress.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	writeJSON(w, http.StatusOK, response)
}
