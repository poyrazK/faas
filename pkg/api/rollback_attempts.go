package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
)

const RollbackAttemptHeader = "Rollback-Attempt-ID"

type rollbackAttemptKey struct{}

// ContextWithRollbackAttempt affects only explicit checked rollback requests.
// Its immutable operation ID is the UUID representation of the opaque attempt.
func ContextWithRollbackAttempt(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, rollbackAttemptKey{}, id)
}
func rollbackAttemptHeaders(ctx context.Context) http.Header {
	id, _ := ctx.Value(rollbackAttemptKey{}).(string)
	if id == "" {
		return nil
	}
	h := make(http.Header)
	h.Set(RollbackAttemptHeader, id)
	return h
}

// RollbackRequestHash binds an exact checked pair and reason. UUID spelling is
// canonicalized because APID accepts compact and dashed deployment identities.
func RollbackRequestHash(appID string, req RollbackRequest) string {
	canonical := func(id string) string {
		if u, err := uuid.Parse(id); err == nil {
			return u.String()
		}
		return id
	}
	if req.TargetDeploymentID != nil {
		id := canonical(*req.TargetDeploymentID)
		req.TargetDeploymentID = &id
	}
	if req.ExpectedCurrentDeploymentID != nil {
		id := canonical(*req.ExpectedCurrentDeploymentID)
		req.ExpectedCurrentDeploymentID = &id
	}
	body, _ := json.Marshal(struct {
		AppID   string
		Request RollbackRequest
	}{canonical(appID), req})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
