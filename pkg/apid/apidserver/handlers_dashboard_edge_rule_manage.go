package apidserver

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-091 amendment 2026-10-30: the dashboard edits a rule, manages the
// account's rule lists and rolls the rule set back. Every form goes through
// the API handler, so validation, plan gates, organization checks and the
// convergence barrier are the API's.

// dashboardEdgeRuleForm verifies the session, the named CSRF token and the
// app, and parses the form. It writes the refusal and reports false.
func (s *server) dashboardEdgeRuleForm(w http.ResponseWriter, r *http.Request) (state.Account, state.App, bool) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return state.Account{}, state.App{}, false
	}
	if !s.verifyDashboardEdgeRulesCSRF(w, r, acct.ID) {
		return state.Account{}, state.App{}, false
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("could not parse edge rule form"))
		return state.Account{}, state.App{}, false
	}
	app, err := s.store.AppBySlug(r.Context(), r.PathValue("slug"))
	if err != nil || !dashboardAppOwner(r, &acct, app) {
		http.NotFound(w, r)
		return state.Account{}, state.App{}, false
	}
	return acct, app, true
}

func dashboardEdgeRulesRedirect(w http.ResponseWriter, r *http.Request, slug, action string) {
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/edge-rules?action="+action, http.StatusSeeOther)
}

// dashboardEditEdgeRule changes a rule's path, methods, priority and action.
func (s *server) dashboardEditEdgeRule(w http.ResponseWriter, r *http.Request) {
	acct, app, ok := s.dashboardEdgeRuleForm(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	rule, err := s.store.GetEdgeRuleByID(r.Context(), id)
	if err != nil || rule.AccountID != acct.ID || rule.AppID != app.ID {
		http.NotFound(w, r)
		return
	}
	var req api.UpdateEdgeRuleRequest
	path := strings.TrimSpace(r.FormValue("match_path"))
	req.MatchPath = &path
	methods := splitDashboardEdgeRuleMethods(r.FormValue("match_methods"))
	req.MatchMethods = &methods
	if raw := strings.TrimSpace(r.FormValue("priority")); raw != "" {
		priority, err := strconv.Atoi(raw)
		if err != nil {
			api.WriteProblem(w, api.ErrValidation("priority must be an integer"))
			return
		}
		req.Priority = &priority
	}
	if raw := strings.TrimSpace(r.FormValue("action")); raw != "" {
		if !json.Valid([]byte(raw)) {
			api.WriteProblem(w, api.ErrValidation("action must be valid JSON"))
			return
		}
		action := json.RawMessage(raw)
		req.Action = &action
	}
	resp := s.forwardDashboardEdgeRuleJSON(r, acct, http.MethodPatch, "/v1/edge-rules/"+url.PathEscape(id), id, req, s.edgeRules.UpdateEdgeRule)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	dashboardEdgeRulesRedirect(w, r, app.Slug, "updated")
}

// dashboardRollbackEdgeRules restores a recorded rule-set version.
func (s *server) dashboardRollbackEdgeRules(w http.ResponseWriter, r *http.Request) {
	acct, app, ok := s.dashboardEdgeRuleForm(w, r)
	if !ok {
		return
	}
	version, err := strconv.Atoi(strings.TrimSpace(r.FormValue("version")))
	if err != nil || version < 1 {
		api.WriteProblem(w, api.ErrValidation("version must be a positive integer"))
		return
	}
	resp := s.forwardDashboardEdgeRuleJSON(r, acct, http.MethodPost, "/v1/apps/"+url.PathEscape(app.Slug)+"/edge-rules/rollback", "",
		api.RollbackEdgeRulesRequest{Version: version}, s.edgeRules.RollbackEdgeRules)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	dashboardEdgeRulesRedirect(w, r, app.Slug, "rolled_back")
}

// dashboardCreateEdgeRuleList creates an account list from the form.
func (s *server) dashboardCreateEdgeRuleList(w http.ResponseWriter, r *http.Request) {
	acct, app, ok := s.dashboardEdgeRuleForm(w, r)
	if !ok {
		return
	}
	req := api.CreateEdgeRuleListRequest{
		Name: strings.TrimSpace(r.FormValue("name")), Kind: strings.TrimSpace(r.FormValue("kind")),
		Description: strings.TrimSpace(r.FormValue("description")), Items: dashboardListItems(r.FormValue("items")),
	}
	resp := s.forwardDashboardEdgeRuleJSON(r, acct, http.MethodPost, "/v1/edge-rule-lists", "", req, s.edgeRules.CreateEdgeRuleList)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	dashboardEdgeRulesRedirect(w, r, app.Slug, "list_created")
}

// dashboardUpdateEdgeRuleList replaces a list's items (and description).
func (s *server) dashboardUpdateEdgeRuleList(w http.ResponseWriter, r *http.Request) {
	acct, app, ok := s.dashboardEdgeRuleForm(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	items := dashboardListItems(r.FormValue("items"))
	description := strings.TrimSpace(r.FormValue("description"))
	req := api.UpdateEdgeRuleListRequest{Items: &items, Description: &description}
	resp := s.forwardDashboardEdgeRuleJSON(r, acct, http.MethodPatch, "/v1/edge-rule-lists/"+url.PathEscape(name), name, req, s.edgeRules.UpdateEdgeRuleList)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	dashboardEdgeRulesRedirect(w, r, app.Slug, "list_updated")
}

// dashboardDeleteEdgeRuleList deletes a list no rule uses.
func (s *server) dashboardDeleteEdgeRuleList(w http.ResponseWriter, r *http.Request) {
	acct, app, ok := s.dashboardEdgeRuleForm(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	resp := s.forwardDashboardEdgeRuleJSON(r, acct, http.MethodDelete, "/v1/edge-rule-lists/"+url.PathEscape(name), name, nil, s.edgeRules.DeleteEdgeRuleList)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	dashboardEdgeRulesRedirect(w, r, app.Slug, "list_deleted")
}

// dashboardListItems splits a textarea into list items, one per line or
// comma, dropping blanks.
func dashboardListItems(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' })
	items := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			items = append(items, f)
		}
	}
	return items
}

// loadDashboardEdgeRuleLists reads the account's lists, with items, through
// the API handlers. A failure leaves the section empty and is logged.
func (s *server) loadDashboardEdgeRuleLists(r *http.Request, log *slog.Logger, acct state.Account) []dashboard.EdgeRuleListPageItem {
	resp := s.forwardDashboardEdgeRuleJSON(r, acct, http.MethodGet, "/v1/edge-rule-lists", "", nil, s.edgeRules.ListEdgeRuleLists)
	var all api.ListEdgeRuleListsResponse
	if resp.Code != http.StatusOK || json.Unmarshal(resp.Body.Bytes(), &all) != nil {
		log.Warn("dashboard edge rules: list rule lists", "account_id", acct.ID, "status", resp.Code)
		return nil
	}
	out := make([]dashboard.EdgeRuleListPageItem, 0, len(all.Lists))
	for _, l := range all.Lists {
		one := s.forwardDashboardEdgeRuleJSON(r, acct, http.MethodGet, "/v1/edge-rule-lists/"+url.PathEscape(l.Name), l.Name, nil, s.edgeRules.GetEdgeRuleList)
		var full api.EdgeRuleListResponse
		if one.Code == http.StatusOK && json.Unmarshal(one.Body.Bytes(), &full) == nil {
			l = full
		}
		out = append(out, dashboard.EdgeRuleListPageItem{
			Name: l.Name, Kind: l.Kind, Description: l.Description, ItemCount: l.ItemCount,
			Items: strings.Join(l.Items, "\n"), UsedBy: len(l.ReferencedBy),
		})
	}
	return out
}

// loadDashboardEdgeRuleVersions reads the app's recorded rule-set versions
// through the API handler.
func (s *server) loadDashboardEdgeRuleVersions(r *http.Request, log *slog.Logger, acct state.Account, app state.App) []dashboard.EdgeRuleVersionPageItem {
	// The dashboard's GET pages are dispatched by path, so {slug} is unset.
	r = r.Clone(r.Context())
	r.SetPathValue("slug", app.Slug)
	resp := s.forwardDashboardEdgeRuleJSON(r, acct, http.MethodGet, "/v1/apps/"+url.PathEscape(app.Slug)+"/edge-rules/versions", "", nil, s.edgeRules.ListEdgeRuleSetVersions)
	var versions []api.EdgeRuleSetVersionResponse
	if resp.Code != http.StatusOK || json.Unmarshal(resp.Body.Bytes(), &versions) != nil {
		log.Warn("dashboard edge rules: list rule-set versions", "app_id", app.ID, "status", resp.Code)
		return nil
	}
	out := make([]dashboard.EdgeRuleVersionPageItem, 0, len(versions))
	for _, v := range versions {
		out = append(out, dashboard.EdgeRuleVersionPageItem{
			Version: v.Version, RuleCount: v.RuleCount, RecordedAt: dashboardJobsTime(v.CreatedAt), Current: v.Current,
		})
	}
	return out
}

// edgeRuleEditActionJSON is the rule's action in the shape PATCH accepts:
// the kind's own object, without the stored {"kind": ..., <kind>: ...} wrapper.
func edgeRuleEditActionJSON(row state.EdgeRule) string {
	stored, err := json.Marshal(row.Action)
	if err != nil {
		return ""
	}
	var union map[string]json.RawMessage
	if json.Unmarshal(stored, &union) != nil {
		return ""
	}
	return string(union[string(row.Kind)])
}
