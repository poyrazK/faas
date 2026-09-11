package main

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

const githubRecoveryDefaultLimit = 100

func (s *server) getGithubRecoveryStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if !validGithubRecoveryStatus(status) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"invalid status", "status must be pending, processing, succeeded, dead, or empty"))
		return
	}
	prob, limit := api.ParseLimit(r.URL.Query().Get("limit"), githubRecoveryDefaultLimit, 500, "github recovery")
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	client, ok := s.githubd.(githubdRecoveryClient)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("githubd recovery service is unavailable"))
		return
	}
	items, err := client.ListRecoveryQueueItems(r.Context(), status, limit)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list GitHub recovery queues"))
		return
	}
	writeJSON(w, http.StatusOK, projectGithubRecoveryItems(items))
}

func (s *server) postGithubDeliveryRetry(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.postGithubRecoveryRetry(w, r, acct, "delivery", r.PathValue("id"))
}

func (s *server) postGithubCheckRetry(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.postGithubRecoveryRetry(w, r, acct, "check_update", r.PathValue("id"))
}

func (s *server) postGithubRecoveryRetry(w http.ResponseWriter, r *http.Request, acct state.Account, kind, rawID string) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	if r.URL.Query().Get("confirm") != "true" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"confirm required", "?confirm=true is required to retry GitHub recovery work"))
		return
	}
	reason, prob := parseRequiredGithubRecoveryReason(r.URL.Query().Get("reason"))
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	targetID := strings.TrimSpace(rawID)
	if _, err := uuid.Parse(targetID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"invalid target id", "target id must be a UUID"))
		return
	}
	client, ok := s.githubd.(githubdRecoveryClient)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("githubd recovery service is unavailable"))
		return
	}
	retried, err := retryGithubRecoveryItem(r, client, kind, targetID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not retry GitHub recovery item"))
		return
	}
	if !retried {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"item is not retryable", "the item was not found, is active, or was already retried"))
		return
	}
	emitGithubRecoveryAudit(r, s, acct, kind, targetID, reason)
	writeJSON(w, http.StatusOK, api.GithubRecoveryRetryResponse{
		OK: true, Kind: kind, TargetID: targetID, Status: "pending",
	})
}

func validGithubRecoveryStatus(status string) bool {
	switch status {
	case "", "pending", "processing", "succeeded", "dead":
		return true
	default:
		return false
	}
}

func parseRequiredGithubRecoveryReason(reason string) (string, *api.Problem) {
	reason = strings.TrimSpace(reason)
	if len(reason) > obsOpsReasonMaxLen || !obsOpsReasonShape.MatchString(reason) {
		return "", api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"invalid reason", "reason is required and must match [a-z0-9_]{1,64}")
	}
	return reason, nil
}

func retryGithubRecoveryItem(r *http.Request, client githubdRecoveryClient, kind, targetID string) (bool, error) {
	if kind == "delivery" {
		return client.RetryWebhookDelivery(r.Context(), targetID)
	}
	return client.RetryCheckUpdate(r.Context(), targetID)
}

func emitGithubRecoveryAudit(r *http.Request, s *server, acct state.Account, kind, targetID, reason string) {
	if s.audit == nil {
		return
	}
	s.audit.Emit(r.Context(), "operator.action.github_"+kind+"_retry", nil, map[string]any{
		"actor": acct.ID, "target_id": targetID, "reason": reason,
	})
}

func projectGithubRecoveryItems(items githubdgrpc.RecoveryQueueItems) api.GithubRecoveryStatusResponse {
	out := api.GithubRecoveryStatusResponse{
		Deliveries:   make([]api.GithubWebhookDeliveryRecord, 0, len(items.Deliveries)),
		CheckUpdates: make([]api.GithubCheckUpdateRecord, 0, len(items.CheckUpdates)),
	}
	for _, item := range items.Deliveries {
		out.Deliveries = append(out.Deliveries, api.GithubWebhookDeliveryRecord{
			DeliveryID: item.DeliveryID, EventType: item.EventType, Status: item.Status,
			Attempts: item.Attempts, NextAttempt: item.NextAttempt, LastError: item.LastError,
			ReceivedAt: item.ReceivedAt, ProcessedAt: item.ProcessedAt, UpdatedAt: item.UpdatedAt,
		})
	}
	for _, item := range items.CheckUpdates {
		out.CheckUpdates = append(out.CheckUpdates, api.GithubCheckUpdateRecord{
			DeploymentID: item.DeploymentID, Generation: item.Generation, Status: item.Status,
			Attempts: item.Attempts, NextAttempt: item.NextAttempt, LastError: item.LastError,
			ProcessedAt: item.ProcessedAt, UpdatedAt: item.UpdatedAt,
		})
	}
	return out
}
