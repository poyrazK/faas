package managedrealtime

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) authenticateRealtimePublisher(r *http.Request) (string, error) {
	if !s.HistoryPreviewEnabled {
		return "", state.ErrRealtimePublisherCredentialDenied
	}
	if _, err := uuid.Parse(r.PathValue("endpoint_id")); err != nil {
		return "", state.ErrRealtimePublisherCredentialDenied
	}
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || !strings.HasPrefix(parts[1], "grp_") {
		return "", state.ErrRealtimePublisherCredentialDenied
	}
	store, ok := s.Host.Store().(state.RealtimePublisherCredentialStore)
	if !ok {
		return "", state.ErrRealtimePublisherCredentialDenied
	}
	_, err := store.AuthenticateRealtimePublisherCredential(r.Context(), r.PathValue("endpoint_id"), parts[1])
	return parts[1], err
}
func (s *Service) RealtimePublisherLimited(next http.HandlerFunc) http.HandlerFunc {
	cfg := middleware.AuthLimitConfig{CountStatuses: []int{http.StatusUnauthorized}, Log: s.Host.Logger()}
	cfg.OnLimited = func(w http.ResponseWriter, r *http.Request) {
		_, err := s.authenticateRealtimePublisher(r)
		if err == nil {
			next(w, r)
			return
		}
		if errors.Is(err, state.ErrRealtimePublisherCredentialDenied) {
			api.WriteProblem(w, api.ErrAuthRateLimited(60))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("publisher authentication unavailable"))
	}
	return middleware.AuthLimitWithLimiter(cfg, s.AuthLimiter)(next).ServeHTTP
}
func (s *Service) PublishWithRealtimePublisherCredential(w http.ResponseWriter, r *http.Request) {
	token, err := s.authenticateRealtimePublisher(r)
	if err != nil {
		s.writeRealtimePublisherError(w, r, err)
		return
	}
	if problem := validateManagedRealtimeChannel(r.PathValue("channel")); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 {
		api.WriteProblem(w, api.ErrRealtimeInvalid("exactly one Idempotency-Key is required"))
		return
	}
	var req state.RealtimePublisherMessageRequest
	if httpkit.DecodeJSONSized(r, &req, api.RealtimePublisherBodyMaxBytes) != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid publish payload"))
		return
	}
	store := s.Host.Store().(state.RealtimePublisherCredentialStore)
	result, err := store.PublishRealtimeWithCredential(r.Context(), r.PathValue("endpoint_id"), r.PathValue("channel"), token, req, keys[0])
	if err != nil {
		s.writeRealtimePublisherError(w, r, err)
		return
	}
	partial := false
	if !result.Replayed {
		partial = s.fanoutRealtimePublisherEvent(r, result.Message)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if result.Replayed {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	httpkit.WriteJSON(w, http.StatusOK, map[string]any{"sequence": strconv.FormatInt(result.Message.Sequence, 10), "durable": true, "replayed": result.Replayed, "partial": partial})
}
func (s *Service) fanoutRealtimePublisherEvent(r *http.Request, msg state.ManagedRealtimeChannelMessage) bool {
	owner, ok := s.Owner.(realtimeRetainedPublishStatus)
	if !ok {
		return true
	}
	result, err := owner.PublishRetainedWithStatus(r.Context(), msg.EndpointID, msg.Channel, realtime.Message{Data: msg.Data, Binary: msg.Binary, Metadata: msg.Metadata, ExpiresAt: msg.ExpiresAt}, msg.Sequence)
	return err != nil || result.Partial || result.NodesUnavailable > 0 || result.QueueFull > 0 || result.Failed > 0
}
