package postgresaccess

import (
	"github.com/onebox-faas/faas/pkg/authz"
	"strings"
)

var actions = map[string]authz.OrgAction{
	"GET /v1/postgres/databases":                                         authz.OrgActionReadPostgres,
	"GET /v1/postgres/capabilities":                                      authz.OrgActionReadPostgres,
	"GET /v1/postgres/databases/{id}":                                    authz.OrgActionReadPostgres,
	"GET /v1/postgres/databases/{id}/bindings":                           authz.OrgActionReadPostgres,
	"GET /v1/postgres/bindings/{id}":                                     authz.OrgActionReadPostgres,
	"GET /v1/postgres/databases/{id}/resizes/{resize_id}":                authz.OrgActionReadPostgres,
	"GET /v1/postgres/databases/{id}/compute-policy-changes/{change_id}": authz.OrgActionReadPostgres,
	"POST /v1/postgres/databases":                                        authz.OrgActionManagePostgres,
	"POST /v1/postgres/databases/{id}/restore":                           authz.OrgActionManagePostgres,
	"POST /v1/postgres/databases/{id}/resize":                            authz.OrgActionManagePostgres,
	"POST /v1/postgres/databases/{id}/compute-policy":                    authz.OrgActionManagePostgres,
	"DELETE /v1/postgres/databases/{id}":                                 authz.OrgActionManagePostgres,
	"POST /v1/postgres/databases/{id}/bindings":                          authz.OrgActionBindPostgres,
	"DELETE /v1/postgres/bindings/{id}":                                  authz.OrgActionBindPostgres,
	"POST /v1/postgres/bindings/{id}/rotate":                             authz.OrgActionManagePostgres,
	"GET /v1/postgres/usage":                                             authz.OrgActionReadPostgresUsage,
}

func Action(pattern string) (authz.OrgAction, bool) {
	action, ok := actions[strings.Replace(pattern, "/v1/orgs/{organization}/postgres/", "/v1/postgres/", 1)]
	return action, ok
}
