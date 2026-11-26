package postgresaccess

import (
	"github.com/onebox-faas/faas/pkg/authz"
	"strings"
)

var actions = map[string]authz.OrgAction{
	"POST /v1/postgres/databases/{id}/imports/{operation_id}/archive":    authz.OrgActionManagePostgres,
	"GET /v1/postgres/databases/{id}/backup-schedule":                    authz.OrgActionManagePostgres,
	"PUT /v1/postgres/databases/{id}/backup-schedule":                    authz.OrgActionManagePostgres,
	"DELETE /v1/postgres/databases/{id}/backup-schedule":                 authz.OrgActionManagePostgres,
	"POST /v1/postgres/databases/{id}/exports":                           authz.OrgActionManagePostgres,
	"GET /v1/postgres/databases/{id}/exports/{export_id}":                authz.OrgActionManagePostgres,
	"GET /v1/postgres/databases/{id}/exports/{export_id}/download":       authz.OrgActionManagePostgres,
	"GET /v1/postgres/databases":                                         authz.OrgActionReadPostgres,
	"GET /v1/postgres/capabilities":                                      authz.OrgActionReadPostgres,
	"GET /v1/postgres/databases/{id}":                                    authz.OrgActionReadPostgres,
	"GET /v1/postgres/databases/{id}/bindings":                           authz.OrgActionReadPostgres,
	"GET /v1/postgres/bindings/{id}":                                     authz.OrgActionReadPostgres,
	"GET /v1/postgres/databases/{id}/resizes/{resize_id}":                authz.OrgActionReadPostgres,
	"GET /v1/postgres/databases/{id}/compute-policy-changes/{change_id}": authz.OrgActionReadPostgres,
	"POST /v1/postgres/databases":                                        authz.OrgActionManagePostgres,
	"POST /v1/postgres/databases/{id}/migration-check":                   authz.OrgActionManagePostgres,
	"POST /v1/postgres/databases/{id}/migration":                         authz.OrgActionManagePostgres,
	"POST /v1/postgres/databases/{id}/migration/stop":                    authz.OrgActionManagePostgres,
	"POST /v1/postgres/databases/{id}/migration/prepare":                 authz.OrgActionManagePostgres,
	"POST /v1/postgres/databases/{id}/migration/verify":                  authz.OrgActionManagePostgres,
	"GET /v1/postgres/databases/{id}/migration":                          authz.OrgActionReadPostgres,
	"POST /v1/postgres/databases/{id}/imports/check":                     authz.OrgActionManagePostgres,
	"POST /v1/postgres/databases/{id}/imports":                           authz.OrgActionManagePostgres,
	"GET /v1/postgres/databases/{id}/imports/{operation_id}":             authz.OrgActionReadPostgres,
	"GET /v1/postgres/databases/{id}/restore-drills/{drill_id}":          authz.OrgActionReadPostgres,
	"GET /v1/postgres/databases/{id}/upgrade-rehearsals/{rehearsal_id}":  authz.OrgActionReadPostgres,
	"POST /v1/postgres/databases/{id}/restore-drills":                    authz.OrgActionManagePostgres,
	"POST /v1/postgres/databases/{id}/upgrade-rehearsals":                authz.OrgActionManagePostgres,
	"GET /v1/postgres/databases/{id}/restore-check":                      authz.OrgActionReadPostgres,
	"GET /v1/postgres/databases/{id}/tables":                             authz.OrgActionReadPostgres,
	"GET /v1/postgres/databases/{id}/sessions":                           authz.OrgActionReadPostgres,
	"POST /v1/postgres/databases/{id}/cancel-query":                      authz.OrgActionManagePostgres,
	"GET /v1/postgres/databases/{id}/diagnostics":                        authz.OrgActionReadPostgres,
	"GET /v1/postgres/databases/{id}/extensions":                         authz.OrgActionReadPostgres,
	"PUT /v1/postgres/databases/{id}/extensions/{extension}":             authz.OrgActionManagePostgres,
	"GET /v1/postgres/databases/{id}/errors":                             authz.OrgActionReadPostgres,
	"PUT /v1/postgres/databases/{id}/errors":                             authz.OrgActionManagePostgres,
	"GET /v1/postgres/databases/{id}/query-insights":                     authz.OrgActionReadPostgres,
	"PUT /v1/postgres/databases/{id}/query-insights":                     authz.OrgActionManagePostgres,
	"GET /v1/postgres/databases/{id}/autovacuum":                         authz.OrgActionReadPostgres,
	"PUT /v1/postgres/databases/{id}/autovacuum":                         authz.OrgActionManagePostgres,
	"POST /v1/postgres/databases/{id}/restore":                           authz.OrgActionManagePostgres,
	"POST /v1/postgres/databases/{id}/masked-branches":                   authz.OrgActionManagePostgres,
	"POST /v1/postgres/databases/{id}/undelete":                          authz.OrgActionManagePostgres,
	"PUT /v1/postgres/databases/{id}/recovery-region":                    authz.OrgActionManagePostgres,
	"PUT /v1/postgres/databases/{id}/maintenance-window":                 authz.OrgActionManagePostgres,
	"PUT /v1/postgres/databases/{id}/auto-grow":                          authz.OrgActionManagePostgres,
	"POST /v1/postgres/databases/{id}/resize":                            authz.OrgActionManagePostgres,
	"POST /v1/postgres/databases/{id}/compute-policy":                    authz.OrgActionManagePostgres,
	"PUT /v1/postgres/databases/{id}/suspend-timeout":                    authz.OrgActionManagePostgres,
	"PUT /v1/postgres/databases/{id}/wake-with-app":                      authz.OrgActionManagePostgres,
	"PUT /v1/postgres/databases/{id}/previews":                           authz.OrgActionManagePostgres,
	"GET /v1/postgres/databases/{id}/roles":                              authz.OrgActionReadPostgres,
	"PUT /v1/postgres/databases/{id}/roles/{name}":                       authz.OrgActionManagePostgres,
	"DELETE /v1/postgres/databases/{id}/roles/{name}":                    authz.OrgActionManagePostgres,
	"DELETE /v1/postgres/databases/{id}":                                 authz.OrgActionManagePostgres,
	"POST /v1/postgres/databases/{id}/bindings":                          authz.OrgActionBindPostgres,
	"DELETE /v1/postgres/bindings/{id}":                                  authz.OrgActionBindPostgres,
	"GET /v1/postgres/bindings/{id}/timeouts":                            authz.OrgActionReadPostgres,
	"PUT /v1/postgres/bindings/{id}/timeouts":                            authz.OrgActionManagePostgres,
	"POST /v1/postgres/bindings/{id}/rotate":                             authz.OrgActionManagePostgres,
	"GET /v1/postgres/usage":                                             authz.OrgActionReadPostgresUsage,
	// Reviewed in bug hunt 37: in no map, so every member of the database's
	// organization, owners included, was refused them.
	"GET /v1/postgres/databases/{id}/status":                authz.OrgActionReadPostgres,
	"GET /v1/postgres/databases/{id}/connection-budget":     authz.OrgActionReadPostgres,
	"GET /v1/postgres/databases/{id}/activity":              authz.OrgActionReadPostgres,
	"GET /v1/postgres/databases/{id}/upgrades/{upgrade_id}": authz.OrgActionReadPostgres,
	"GET /v1/postgres/overage":                              authz.OrgActionReadPostgresUsage,
	"PUT /v1/postgres/overage":                              authz.OrgActionManagePostgres,
	"PATCH /v1/postgres/databases/{id}/labels":              authz.OrgActionManagePostgres,
	"POST /v1/postgres/databases/{id}/upgrade":              authz.OrgActionManagePostgres,
	// Live queries carry a signing secret (rotate), so even reading them is
	// the database administrators'.
	"GET /v1/postgres/databases/{id}/live":         authz.OrgActionManagePostgres,
	"POST /v1/postgres/databases/{id}/live":        authz.OrgActionManagePostgres,
	"POST /v1/postgres/databases/{id}/live/rotate": authz.OrgActionManagePostgres,
	"DELETE /v1/postgres/databases/{id}/live":      authz.OrgActionManagePostgres,
	// Read-only queries return rows: the members who may bind an app to the
	// database, and so read its rows through that app (ADR-20261117215858).
	"POST /v1/postgres/databases/{id}/query": authz.OrgActionBindPostgres,
	// The Data API catalog preview reads the database's tables and columns:
	// the same members as a query.
	"POST /v1/postgres/databases/{id}/data-api-catalog-preview": authz.OrgActionBindPostgres,
}

func Action(pattern string) (authz.OrgAction, bool) {
	action, ok := actions[strings.Replace(pattern, "/v1/orgs/{organization}/postgres/", "/v1/postgres/", 1)]
	return action, ok
}
