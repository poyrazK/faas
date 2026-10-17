package promotion

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// WriteAdmissionError preserves definite rejection for a full waiting queue.
// Admission and approval consumption share one transaction, so this response
// leaves the request's approval available for a later retry.
func WriteAdmissionError(w http.ResponseWriter, err error) bool {
	var full *state.PromotionAdmissionFullError
	if errors.As(err, &full) {
		w.Header().Set("Retry-After", strconv.Itoa(api.ProjectPromotionAdmissionRetryAfterSeconds))
		api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, api.CodeProjectPromotionQueueFull,
			"Promotion queue is full", "No promotion was accepted; retry the same request after capacity becomes available.").
			WithLimit(full.Limit, full.Observed).WithDocs("https://gregale.dev/docs/deploys#promotion-admission"))
		return true
	}
	var approval *state.PromotionApprovalError
	if errors.As(err, &approval) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeProjectEnvironmentApprovalInvalid,
			"Invalid environment approval", "The approval is expired or does not match this exact promotion."))
		return true
	}
	return false
}

// RecordAdmissionApproval emits a receipt only for the transaction that accepted
// and consumed approval. Replays and rejected requests have no receipt.
func RecordAdmissionApproval(ctx context.Context, audit interface {
	Emit(context.Context, string, *string, map[string]any)
}, approval *state.ProjectEnvironmentApproval) {
	if approval == nil || approval.ConsumedAt == nil {
		return
	}
	audit.Emit(ctx, "project.environment.approval.consumed", &approval.AccountID, map[string]any{
		"project_slug": approval.ProjectSlug, "environment": approval.EnvironmentSlug, "approval_id": approval.ID, "token_kind": approval.TokenKind, "consumed_at": approval.ConsumedAt.UTC().Format(time.RFC3339),
	})
}
