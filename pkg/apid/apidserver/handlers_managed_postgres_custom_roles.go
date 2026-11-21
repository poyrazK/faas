package apidserver

import (
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// Custom Postgres group roles (ADR-20261116213000): a customer declares
// NOLOGIN roles on a managed database and grants them to its own logins.

const customRolesBodyLimit = 16 << 10

func managedPostgresCustomRoleView(role managedpostgres.CustomRole) api.ManagedPostgresCustomRole {
	grants := make([]string, 0, len(role.GrantedTo))
	for _, grant := range role.GrantedTo {
		grants = append(grants, string(grant))
	}
	memberOf := append([]string{}, role.MemberOf...)
	defaults := make([]string, 0, len(role.DefaultFor))
	for _, access := range role.DefaultFor {
		defaults = append(defaults, string(access))
	}
	return api.ManagedPostgresCustomRole{
		DatabaseID: role.DatabaseID, Name: role.Name, BypassRLS: role.BypassRLS, GrantedTo: grants, MemberOf: memberOf, DefaultFor: defaults,
		State: string(role.State), LastErrorCode: role.LastError,
		CreatedAt: role.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: role.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

// customRolesService gates the request on the release flag and the plan.
func (s *server) customRolesService(w http.ResponseWriter, acct state.Account, mutate bool) (*managedpostgres.CustomRoleService, bool) {
	if s.managedPostgresBindings == nil {
		managedPostgresNotConfiguredProblem(w)
		return nil, false
	}
	if !s.platformFlagEnabled(platformflags.PostgresCustomRoles, acct) {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "postgres_custom_roles_unavailable",
			"Postgres custom roles unavailable", "Custom Postgres roles are not released to this account yet"))
		return nil, false
	}
	if mutate && api.ManagedPostgresCustomRolesMax(acct.Plan) == 0 {
		api.WriteProblem(w, api.ErrPlanFeatureGated("postgres_custom_roles", acct.Plan).
			WithHint("Custom Postgres roles are on Hobby and above."))
		return nil, false
	}
	service, err := s.managedPostgresBindings.CustomRoles()
	if err != nil {
		managedPostgresNotConfiguredProblem(w)
		return nil, false
	}
	return service, true
}

// customRoleProblem names the custom-role refusals before the generic
// managed PostgreSQL mapping.
func customRoleProblem(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, managedpostgres.ErrBypassRLSConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "postgres_bypass_rls_conflict", "BYPASSRLS conflicts with Data API or Auth",
			"A BYPASSRLS custom role cannot exist on a database that uses Data API or Auth, which rely on row-level security.").
			WithHint("Use a separate database for the tool that needs BYPASSRLS, or declare the role without --bypass-rls.").
			WithDocs("https://gregale.dev/docs/postgres-custom-roles"))
	case errors.Is(err, managedpostgres.ErrBypassRLSUnavailable):
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, "postgres_bypass_rls_unavailable", "BYPASSRLS unavailable",
			"This database cannot create BYPASSRLS roles.").WithDocs("https://gregale.dev/docs/postgres-custom-roles"))
	case errors.Is(err, managedpostgres.ErrCustomRolesUnsupported):
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, "postgres_custom_roles_unsupported", "Custom roles unsupported",
			"This database's backend does not support custom roles."))
	case errors.Is(err, managedpostgres.ErrCustomRoleDefaultTaken):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "postgres_custom_role_default_taken", "Default role already set",
			"Another custom role is already the default role of that access mode; a login starts as at most one role.").
			WithHint("Remove it there first (gregale postgres roles create <database> <role> --default-for '' on that role).").
			WithDocs("https://gregale.dev/docs/postgres-custom-roles"))
	case errors.Is(err, managedpostgres.ErrQuotaExceeded):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "postgres_custom_roles_limit", "Custom role limit reached",
			"A database can declare at most 16 custom roles.").WithHint("Delete a role you no longer need (gregale postgres roles delete)."))
	default:
		managedPostgresProblem(w, err)
	}
}

func (s *server) listManagedPostgresCustomRoles(w http.ResponseWriter, r *http.Request, acct state.Account) {
	service, ok := s.customRolesService(w, acct, false)
	if !ok {
		return
	}
	roles, err := service.List(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		customRoleProblem(w, err)
		return
	}
	items := make([]api.ManagedPostgresCustomRole, 0, len(roles))
	for _, role := range roles {
		items = append(items, managedPostgresCustomRoleView(role))
	}
	writeJSON(w, http.StatusOK, api.ManagedPostgresCustomRoleList{Items: items})
}

func accessModes(values []string) []managedpostgres.CredentialAccess {
	out := make([]managedpostgres.CredentialAccess, 0, len(values))
	for _, value := range values {
		out = append(out, managedpostgres.CredentialAccess(value))
	}
	return out
}

func customRoleSaveRequest(accountID, databaseID, name string, req api.PutManagedPostgresCustomRoleRequest) managedpostgres.SaveCustomRoleRequest {
	request := managedpostgres.SaveCustomRoleRequest{
		AccountID: accountID, DatabaseID: databaseID, Name: name,
		BypassRLS: req.BypassRLS, GrantedTo: accessModes(req.GrantedTo), MemberOf: req.MemberOf,
	}
	if req.DefaultFor != nil {
		defaults := accessModes(*req.DefaultFor)
		request.DefaultFor = &defaults
	}
	return request
}

func (s *server) putManagedPostgresCustomRole(w http.ResponseWriter, r *http.Request, acct state.Account) {
	service, ok := s.customRolesService(w, acct, true)
	if !ok {
		return
	}
	var req api.PutManagedPostgresCustomRoleRequest
	if !decodeJSONLimit(w, r, &req, customRolesBodyLimit) {
		return
	}
	role, err := service.Save(r.Context(), customRoleSaveRequest(acct.ID, r.PathValue("id"), r.PathValue("name"), req))
	if err != nil {
		customRoleProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "managed_postgres.custom_role.saved", &acct.ID, map[string]any{
		"database_id": role.DatabaseID, "name": role.Name, "bypass_rls": role.BypassRLS,
		"granted_to": req.GrantedTo, "member_of": role.MemberOf, "default_for": role.DefaultFor, "state": string(role.State),
	})
	status := http.StatusOK
	if role.State != managedpostgres.CustomRoleReady {
		status = http.StatusAccepted
	}
	writeJSON(w, status, managedPostgresCustomRoleView(role))
}

func (s *server) deleteManagedPostgresCustomRole(w http.ResponseWriter, r *http.Request, acct state.Account) {
	service, ok := s.customRolesService(w, acct, false)
	if !ok {
		return
	}
	role, err := service.Delete(r.Context(), acct.ID, r.PathValue("id"), r.PathValue("name"))
	if err != nil {
		customRoleProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "managed_postgres.custom_role.deleted", &acct.ID, map[string]any{
		"database_id": role.DatabaseID, "name": role.Name,
	})
	writeJSON(w, http.StatusAccepted, managedPostgresCustomRoleView(role))
}
