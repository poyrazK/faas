package managedrealtime

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) authenticateRealtimeWorker(r *http.Request) (state.RealtimeWorkerGrant, error) {
	var grant state.RealtimeWorkerGrant
	if !s.HistoryPreviewEnabled {
		return grant, state.ErrRealtimeWorkerCredentialDenied
	}
	if _, err := uuid.Parse(r.PathValue("group_id")); err != nil {
		return grant, state.ErrRealtimeWorkerCredentialDenied
	}
	fields := strings.Fields(r.Header.Get("Authorization"))
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") || !strings.HasPrefix(fields[1], "grw_") {
		return grant, state.ErrRealtimeWorkerCredentialDenied
	}
	store, ok := s.Host.Store().(state.RealtimeWorkerCredentialStore)
	if !ok {
		return grant, state.ErrRealtimeWorkerCredentialDenied
	}
	return store.AuthenticateRealtimeWorkerCredential(r.Context(), r.PathValue("group_id"), fields[1])
}
func (s *Service) RealtimeWorkerLimited(next http.HandlerFunc) http.HandlerFunc {
	cfg := middleware.AuthLimitConfig{CountStatuses: []int{http.StatusUnauthorized}, Log: s.Host.Logger()}
	cfg.OnLimited = func(w http.ResponseWriter, r *http.Request) {
		_, err := s.authenticateRealtimeWorker(r)
		if err == nil {
			next(w, r)
			return
		}
		if errors.Is(err, state.ErrRealtimeWorkerCredentialDenied) {
			api.WriteProblem(w, api.ErrAuthRateLimited(60))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("worker authentication unavailable"))
	}
	return middleware.AuthLimitWithLimiter(cfg, s.AuthLimiter)(next).ServeHTTP
}
func (s *Service) loadRealtimeWorker(w http.ResponseWriter, r *http.Request, operation string) (state.RealtimeWorkerGrant, state.RealtimeConsumerGroupStore, bool) {
	grant, err := s.authenticateRealtimeWorker(r)
	if err != nil {
		if errors.Is(err, state.ErrRealtimeWorkerCredentialDenied) {
			api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Invalid worker credential", "supply a current credential bound to this group"))
		} else {
			api.WriteProblem(w, api.ErrCapacity("worker authentication unavailable"))
		}
		return grant, nil, false
	}
	allowed := false
	for _, permission := range grant.Info.Permissions {
		if permission == operation {
			allowed = true
		}
	}
	if !allowed {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Worker permission denied", "this credential does not grant the requested operation"))
		return grant, nil, false
	}
	acct, err := s.Host.Store().AccountByID(r.Context(), grant.AccountID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("worker account unavailable"))
		return grant, nil, false
	}
	if _, ok := s.realtimePlanLimits(acct); !ok {
		api.WriteProblem(w, api.ErrPlanRealtimeNotAllowed(acct.Plan))
		return grant, nil, false
	}
	store, ok := s.Host.Store().(state.RealtimeConsumerGroupStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("worker groups unavailable"))
		return grant, nil, false
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	return grant, store, true
}
func (s *Service) RealtimeWorkerStatus(w http.ResponseWriter, r *http.Request) {
	g, store, ok := s.loadRealtimeWorker(w, r, "status")
	if !ok {
		return
	}
	workerCtx := state.WithRealtimeWorkerGrant(r.Context(), g, "status")
	r = r.WithContext(workerCtx)
	groups, err := store.ListRealtimeConsumerGroups(workerCtx, g.EndpointID, g.Channel)
	if err != nil {
		s.writeRealtimeWorkerOperationError(w, r, err)
		return
	}
	for _, group := range groups {
		if group.GroupID == g.Info.GroupID {
			httpkit.WriteJSON(w, http.StatusOK, group)
			return
		}
	}
	httpkit.NotFound(w, "group not found")
}
func (s *Service) RealtimeWorkerClaim(w http.ResponseWriter, r *http.Request) {
	g, store, ok := s.loadRealtimeWorker(w, r, "claim")
	if !ok {
		return
	}
	r = r.WithContext(state.WithRealtimeWorkerGrant(r.Context(), g, "claim"))
	var req struct {
		Worker      string `json:"worker,omitempty"`
		WaitSeconds int    `json:"wait_seconds,omitempty"`
	}
	if httpkit.DecodeJSONSized(r, &req, api.RealtimeConsumerGroupBodyMaxBytes) != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid worker claim request"))
		return
	}
	if req.Worker != "" && req.Worker != g.Info.Name {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Worker identity denied", "worker identity is fixed by the credential"))
		return
	}
	claim, err := s.claimRealtimeGroupWithWait(r, store, g.EndpointID, g.Channel, g.Group, g.Info.Name, req.WaitSeconds)
	if err != nil {
		s.writeRealtimeWorkerOperationError(w, r, err)
		return
	}
	if claim == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, claim)
}
func (s *Service) RealtimeWorkerSettle(w http.ResponseWriter, r *http.Request) {
	var req state.RealtimeConsumerGroupSettlement
	// Authenticate before parsing so unauthenticated requests always count toward
	// the auth budget. The action's exact permission is then checked separately.
	if _, err := s.authenticateRealtimeWorker(r); err != nil {
		s.writeRealtimeWorkerOperationError(w, r, err)
		return
	}
	if httpkit.DecodeJSONSized(r, &req, api.RealtimeConsumerGroupBodyMaxBytes) != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid claim settlement"))
		return
	}
	if req.Action != "complete" && req.Action != "release" && req.Action != "renew" && req.Action != "retry" {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid settlement action"))
		return
	}
	g, store, ok := s.loadRealtimeWorker(w, r, req.Action)
	if !ok {
		return
	}
	workerCtx := state.WithRealtimeWorkerGrant(r.Context(), g, req.Action)
	r = r.WithContext(workerCtx)
	result, err := store.SettleRealtimeConsumerGroup(workerCtx, g.EndpointID, g.Channel, g.Group, req)
	if err != nil {
		s.writeRealtimeWorkerOperationError(w, r, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, result)
}
func (s *Service) RealtimeWorkerDeadLetters(w http.ResponseWriter, r *http.Request) {
	g, store, ok := s.loadRealtimeWorker(w, r, "dead_letters")
	if !ok {
		return
	}
	workerCtx := state.WithRealtimeWorkerGrant(r.Context(), g, "dead_letters")
	r = r.WithContext(workerCtx)
	letters, err := store.ListRealtimeConsumerGroupDeadLetters(workerCtx, g.EndpointID, g.Channel, g.Group)
	if err != nil {
		s.writeRealtimeWorkerOperationError(w, r, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, letters)
}
func (s *Service) RealtimeWorkerReplay(w http.ResponseWriter, r *http.Request) {
	g, store, ok := s.loadRealtimeWorker(w, r, "replay")
	if !ok {
		return
	}
	workerCtx := state.WithRealtimeWorkerGrant(r.Context(), g, "replay")
	r = r.WithContext(workerCtx)
	err := store.ReplayRealtimeConsumerGroupDeadLetter(workerCtx, g.EndpointID, g.Channel, g.Group, r.PathValue("sequence"))
	if err != nil {
		s.writeRealtimeWorkerOperationError(w, r, err)
		return
	}
	s.auditRealtimeWorkerAdminOperation(r, g, "replay")
	w.WriteHeader(http.StatusNoContent)
}
func (s *Service) RealtimeWorkerResync(w http.ResponseWriter, r *http.Request) {
	g, store, ok := s.loadRealtimeWorker(w, r, "resync")
	if !ok {
		return
	}
	workerCtx := state.WithRealtimeWorkerGrant(r.Context(), g, "resync")
	r = r.WithContext(workerCtx)
	var req struct {
		ExpectedSequence string `json:"expected_sequence"`
		AfterSequence    string `json:"after_sequence"`
	}
	if httpkit.DecodeJSONSized(r, &req, api.RealtimeConsumerGroupBodyMaxBytes) != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid group recovery"))
		return
	}
	err := store.ResetRealtimeConsumerGroup(workerCtx, g.EndpointID, g.Channel, g.Group, req.ExpectedSequence, req.AfterSequence)
	if err != nil {
		s.writeRealtimeWorkerOperationError(w, r, err)
		return
	}
	s.auditRealtimeWorkerAdminOperation(r, g, "resync")
	w.WriteHeader(http.StatusNoContent)
}
func (s *Service) RealtimeWorkerDelete(w http.ResponseWriter, r *http.Request) {
	g, store, ok := s.loadRealtimeWorker(w, r, "delete")
	if !ok {
		return
	}
	workerCtx := state.WithRealtimeWorkerGrant(r.Context(), g, "delete")
	r = r.WithContext(workerCtx)
	if err := store.DeleteRealtimeConsumerGroup(workerCtx, g.EndpointID, g.Channel, g.Group); err != nil {
		s.writeRealtimeWorkerOperationError(w, r, err)
		return
	}
	s.auditRealtimeWorkerAdminOperation(r, g, "delete")
	w.WriteHeader(http.StatusNoContent)
}
func (s *Service) writeRealtimeWorkerOperationError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, state.ErrRealtimeWorkerCredentialDenied) {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Worker credential unavailable", "credential expired, was revoked or rotated, or no longer grants this operation"))
		return
	}
	s.writeRealtimeConsumerGroupError(w, r, err)
}
func (s *Service) auditRealtimeWorkerAdminOperation(r *http.Request, g state.RealtimeWorkerGrant, action string) {
	s.Host.EmitAudit(r.Context(), "realtime.worker_"+action, &g.AccountID, map[string]any{"group_id": g.Info.GroupID, "credential_id": g.Info.ID})
}
