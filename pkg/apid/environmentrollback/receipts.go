// Package environmentrollback handles immutable environment rollback acceptance.
package environmentrollback

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func Attempt(ctx context.Context, h http.Header) (context.Context, *api.Problem) {
	ids, hashes := h.Values(api.EnvironmentRollbackAttemptHeader), h.Values(api.EnvironmentRollbackHashHeader)
	if len(ids) == 0 && len(hashes) == 0 {
		return ctx, nil
	}
	if len(ids) != 1 || len(hashes) != 1 || !api.ValidDeploymentAttemptID(ids[0]) || len(hashes[0]) != 64 || strings.Trim(hashes[0], "0123456789abcdef") != "" {
		return ctx, api.ErrValidation("environment rollback acceptance requires one lowercase 32-hex attempt ID and one lowercase 64-hex original request hash")
	}
	return state.ContextWithEnvironmentRollbackAttempt(ctx, ids[0], hashes[0]), nil
}
func Problem(err error) *api.Problem {
	if errors.Is(err, state.ErrEnvironmentRollbackAttemptAccepted) {
		return api.NewProblem(http.StatusConflict, "environment_rollback_attempt_accepted", "Environment rollback already accepted", "Read the original acceptance receipt and observe restoration separately; do not replay the rollback.")
	}
	if errors.Is(err, state.ErrEnvironmentRollbackAttemptConflict) {
		return api.NewProblem(http.StatusConflict, "environment_rollback_attempt_conflict", "Environment rollback attempt conflict", "The original request or environment identity differs, or a rollback was already admitted without this attempt.")
	}
	return nil
}
func Read(w http.ResponseWriter, r *http.Request, accountID string, store state.Store) {
	if _, err := uuid.Parse(r.PathValue("promotion")); err != nil {
		api.WriteProblem(w, api.ErrValidation("promotion must be its original UUID"))
		return
	}
	attempt := r.PathValue("attempt_id")
	if !api.ValidDeploymentAttemptID(attempt) {
		api.WriteProblem(w, api.ErrValidation("attempt_id must be 32 lowercase hexadecimal characters"))
		return
	}
	receipts, ok := store.(state.EnvironmentRollbackReceiptStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("environment rollback receipts unavailable"))
		return
	}
	receipt, err := receipts.EnvironmentRollbackReceipt(r.Context(), accountID, r.PathValue("slug"), r.PathValue("environment"), r.PathValue("promotion"), attempt)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(404, api.CodeNotFound, "Environment rollback receipt not found", "No retained receipt matches this original environment rollback; acceptance remains uncertain."))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read environment rollback receipt"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(receipt)
}

// BoundedIdempotencyKey preserves every previously storable key, including
// Unicode keys. Company scope prefixes can push an otherwise valid header
// beyond the ledger's character limit; those keys never committed previously.
func BoundedIdempotencyKey(key string) string {
	if utf8.RuneCountInString(key) <= 255 {
		return key
	}
	sum := sha256.Sum256([]byte(key))
	return "environment-rollback-v1:" + hex.EncodeToString(sum[:])
}
