package promotion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func EnvironmentAttempt(ctx context.Context, h http.Header) (context.Context, *api.Problem) {
	ids, hashes := h.Values(api.EnvironmentPromotionAttemptHeader), h.Values(api.EnvironmentPromotionHashHeader)
	if len(ids) == 0 && len(hashes) == 0 {
		return ctx, nil
	}
	if len(ids) != 1 || len(hashes) != 1 || !api.ValidDeploymentAttemptID(ids[0]) || len(hashes[0]) != 64 || strings.Trim(hashes[0], "0123456789abcdef") != "" {
		return ctx, api.ErrValidation("environment promotion acceptance requires one lowercase 32-hex attempt ID and one lowercase 64-hex original request hash")
	}
	return state.ContextWithEnvironmentPromotionAttempt(ctx, ids[0], hashes[0]), nil
}
func EnvironmentAttemptProblem(err error) *api.Problem {
	if errors.Is(err, state.ErrEnvironmentPromotionAttemptAccepted) {
		return api.NewProblem(http.StatusConflict, "environment_promotion_attempt_accepted", "Environment promotion already accepted", "Read the original acceptance receipt and observe restoration separately; do not replay the promotion.")
	}
	if errors.Is(err, state.ErrEnvironmentPromotionAttemptConflict) {
		return api.NewProblem(http.StatusConflict, "environment_promotion_attempt_conflict", "Environment promotion attempt conflict", "The original request or environment identity differs, or a promotion was already admitted without this attempt.")
	}
	return nil
}
func ReadEnvironmentReceipt(w http.ResponseWriter, r *http.Request, accountID string, store state.Store) {
	attempt := r.PathValue("attempt_id")
	if !api.ValidDeploymentAttemptID(attempt) {
		api.WriteProblem(w, api.ErrValidation("attempt_id must be 32 lowercase hexadecimal characters"))
		return
	}
	receipts, ok := store.(state.EnvironmentPromotionReceiptStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("environment promotion receipts unavailable"))
		return
	}
	receipt, err := receipts.EnvironmentPromotionReceipt(r.Context(), accountID, r.PathValue("slug"), r.PathValue("environment"), attempt)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(404, api.CodeNotFound, "Environment promotion receipt not found", "No retained receipt matches this original environment promotion; acceptance remains uncertain."))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read environment promotion receipt"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(receipt)
}

// BoundedIdempotencyKey preserves every previously storable key, including
// Unicode keys. Company scope prefixes can push an otherwise valid header
// beyond the ledger's character limit; those keys never committed previously.
func EnvironmentIdempotencyKey(key string) string {
	if utf8.RuneCountInString(key) <= 255 {
		return key
	}
	sum := sha256.Sum256([]byte(key))
	return "environment-promotion-v1:" + hex.EncodeToString(sum[:])
}

// EnvironmentExistingAttemptProblem preserves legacy explicit continuation and
// refuses modern re-admission before any native dispatch or approval consumption.
func EnvironmentExistingAttemptProblem(ctx context.Context, store state.Store, p state.ProjectEnvironmentPromotion) *api.Problem {
	if !state.HasEnvironmentPromotionAttempt(ctx) {
		return nil
	}
	receipts, ok := store.(state.EnvironmentPromotionReceiptStore)
	if !ok {
		return api.ErrCapacity("environment promotion acceptance unavailable")
	}
	err := receipts.CheckEnvironmentPromotionAttempt(ctx, p)
	if problem := EnvironmentAttemptProblem(err); problem != nil {
		return problem
	}
	return api.ErrCapacity("could not verify original environment promotion acceptance")
}
