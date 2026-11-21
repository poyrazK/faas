package apidserver

import (
	"net/http"

	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/logsanitize"
)

// requestAuditActor names who made r for an audit row: "api:<key id>" for an
// API key (with the key id and label in data) or "dashboard:<account>" for a
// session. Without it, rows such as app.deleted named only the daemon
// ("apid"), so nobody could tell which key deleted an app (prod 2026-11-15).
// A request without an authenticated account keeps the daemon actor.
func requestAuditActor(r *http.Request, accountID string, data map[string]any) string {
	_, key, ok := authmw.AccountFromContext(r)
	if !ok {
		return auditActor
	}
	if key == nil {
		data["actor_via"] = "dashboard"
		return "dashboard:" + accountID
	}
	keyID := key.ID
	if keyID == "" {
		keyID = "unknown"
	}
	data["actor_via"] = "api"
	data["actor_key_id"] = keyID
	if label := logsanitize.Field(key.Label); label != "" {
		data["actor_key_label"] = label
	}
	return "api:" + keyID
}
