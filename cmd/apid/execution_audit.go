package main

import (
	"net/http"
	"time"

	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
)

// auditExecutionRequest records lifecycle intent without placing source,
// input, output, artifact bytes, or host details in the audit payload.
func (s *server) auditExecutionRequest(r *http.Request, accountID, kind string, row state.Execution) {
	actor := auditActor
	data := map[string]any{
		"execution_id": row.ID,
		"runtime":      string(row.Runtime),
		"profile":      string(row.Profile.Normalized()),
	}
	if _, key, ok := authmw.AccountFromContext(r); ok {
		if key != nil {
			keyID := key.ID
			if keyID == "" {
				keyID = "unknown"
			}
			actor = "api:" + keyID
			data["actor_via"] = "api"
			data["actor_key_id"] = keyID
			if label := logsanitize.Field(key.Label); label != "" {
				data["actor_key_label"] = label
			}
		} else {
			actor = "dashboard:" + accountID
			data["actor_via"] = "dashboard"
		}
	}
	s.audit.EmitAs(r.Context(), actor, kind, &accountID, data)
}

func (s *server) auditExecutionArtifactGrant(r *http.Request, accountID, kind string, grant state.ExecutionArtifactGrant, importingExecutionID string) {
	actor := auditActor
	data := map[string]any{
		"grant_id":            grant.ID,
		"source_execution_id": grant.SourceExecutionID,
		"artifact_name":       grant.ArtifactName,
	}
	if !grant.ExpiresAt.IsZero() {
		data["expires_at"] = grant.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	if importingExecutionID != "" {
		data["execution_id"] = importingExecutionID
	}
	if _, key, ok := authmw.AccountFromContext(r); ok {
		if key != nil {
			keyID := key.ID
			if keyID == "" {
				keyID = "unknown"
			}
			actor = "api:" + keyID
			data["actor_via"] = "api"
			data["actor_key_id"] = keyID
			if label := logsanitize.Field(key.Label); label != "" {
				data["actor_key_label"] = label
			}
		} else {
			actor = "dashboard:" + accountID
			data["actor_via"] = "dashboard"
		}
	}
	s.audit.EmitAs(r.Context(), actor, kind, &accountID, data)
}
