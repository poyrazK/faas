package edgerules

// ADR-963: reusable edge-rule lists. Account-scoped named sets referenced
// from match conditions with the in_list op.

import (
	"context"
	"errors"
	"net/http"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) edgeRuleListStore(w http.ResponseWriter) (state.EdgeRuleListStore, bool) {
	store, ok := s.Host.Store().(state.EdgeRuleListStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("edge rule lists are unavailable"))
	}
	return store, ok
}

// validateEdgeRuleMatchLists resolves a condition's list references against
// the account: an unknown list, or one whose kind does not fit its field, is
// a validation error. Conditions without references need no lookup.
//
// A rule names only lists of its app's scope (ADR-091 amendment 2026-11-04,
// review round 10): a company app's rule names its organization's lists (and
// personal lists only its organization's apps already use), and a personal
// app's rule names personal lists. Otherwise a member could make the app
// creator's private list readable to the organization by naming it, learn
// which private list names exist, or freeze an organization list by naming
// it from a personal rule. A list outside the scope reads as unknown.
func (s *Service) validateEdgeRuleMatchLists(ctx context.Context, accountID string, app state.App, expr *api.EdgeRuleMatchExpr) *api.Problem {
	names := api.EdgeRuleMatchListRefs(expr)
	if len(names) == 0 {
		return nil
	}
	store, ok := s.Host.Store().(state.EdgeRuleListStore)
	if !ok {
		return api.ErrValidation("edge rule lists are unavailable")
	}
	rows, err := store.EdgeRuleListsByName(ctx, accountID, names)
	if err != nil {
		return api.ErrCapacity("could not read edge rule lists")
	}
	orgID, err := s.companyOrgID(ctx, app)
	if err != nil {
		return api.ErrCapacity("could not read the app's organization")
	}
	v := listVisibility{s: s, scope: listScope{orgID: orgID}}
	lists := make(api.EdgeRuleLists, len(rows))
	for _, l := range rows {
		if orgID != "" && l.OrgID == "" && v.refs == nil {
			if v.refs, err = store.EdgeRuleListReferences(ctx, accountID); err != nil {
				return api.ErrCapacity("could not read edge rule list references")
			}
		}
		visible, err := v.visible(ctx, l)
		if err != nil {
			return api.ErrCapacity("could not read the apps that use edge rule lists")
		}
		if !visible {
			continue
		}
		// Kind is all validation needs; items were validated on write.
		compiled, err := api.CompileEdgeRuleList(l.Kind, nil)
		if err != nil {
			return api.ErrCapacity("stored edge rule list is invalid")
		}
		lists[l.Name] = compiled
	}
	prob := api.ValidateEdgeRuleMatchWithLists(expr, lists)
	if prob != nil && orgID != "" {
		for _, name := range names {
			if _, ok := lists[name]; !ok {
				// Lists live with the account that owns a company app's
				// resources, so a list made through another member's app
				// is not this app's (ADR-091 amendment 2026-11-04).
				prob.Detail += "; a company app's rules use the lists created for it: gregale edge rule-list create --app " + app.Slug
				break
			}
		}
	}
	return prob
}

func EdgeRuleListResponse(l state.EdgeRuleList, refs []state.EdgeRuleListRef, withItems bool) api.EdgeRuleListResponse {
	out := api.EdgeRuleListResponse{
		ID: l.ID, Name: l.Name, Kind: l.Kind, Description: l.Description,
		ItemCount: len(l.Items), ReferencedBy: make([]string, 0, len(refs)),
		CreatedAt: l.CreatedAt, UpdatedAt: l.UpdatedAt,
	}
	if withItems {
		out.Items = l.Items
	}
	for _, ref := range refs {
		out.ReferencedBy = append(out.ReferencedBy, ref.RuleID)
	}
	return out
}

// GET /v1/edge-rule-lists
func (s *Service) ListEdgeRuleLists(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.edgeRuleListStore(w)
	if !ok {
		return
	}
	lists, err := store.ListEdgeRuleLists(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read edge rule lists"))
		return
	}
	refs, err := store.EdgeRuleListReferences(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read edge rule list references"))
		return
	}
	v := listVisibility{s: s, scope: listScopeFor(r), refs: refs}
	out := api.ListEdgeRuleListsResponse{Lists: make([]api.EdgeRuleListResponse, 0, len(lists))}
	for _, l := range lists {
		visible, err := v.visible(r.Context(), l)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not read the apps that use edge rule lists"))
			return
		}
		if visible {
			out.Lists = append(out.Lists, EdgeRuleListResponse(l, refs[l.Name], false))
		}
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// GET /v1/edge-rule-lists/{name}
func (s *Service) GetEdgeRuleList(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.edgeRuleListStore(w)
	if !ok {
		return
	}
	l, refs, prob := s.scopedEdgeRuleList(r.Context(), store, listScopeFor(r), acct.ID, r.PathValue("name"))
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, EdgeRuleListResponse(l, refs, true))
}

// validateEdgeRuleListItems normalizes items and enforces the plan's
// per-list item cap. The plan is the one of the account the request acts as:
// for a company app's list, the account its resources belong to, so the
// organization's plan rather than its creator's personal one (ADR-091
// amendment 2026-11-03, review round 9).
func validateEdgeRuleListItems(plan api.Plan, kind string, items []string) ([]string, *api.Problem) {
	normalized, err := api.NormalizeEdgeRuleListItems(kind, items)
	if err != nil {
		return nil, api.ErrValidation(err.Error())
	}
	limits := api.MustLimitsFor(plan)
	if len(normalized) > limits.EdgeRuleListMaxItems {
		return nil, api.ErrPlanLimitEdgeRuleListItems(plan, limits.EdgeRuleListMaxItems, len(normalized))
	}
	return normalized, nil
}

func validateEdgeRuleListDescription(description string) *api.Problem {
	if len(description) > api.EdgeRuleListMaxDescriptionBytes {
		return api.ErrValidation("description longer than 500 bytes")
	}
	return nil
}

// POST /v1/edge-rule-lists
func (s *Service) CreateEdgeRuleList(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateEdgeRuleListRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if err := api.ValidateEdgeRuleListName(req.Name); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if prob := validateEdgeRuleListDescription(req.Description); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	items, prob := validateEdgeRuleListItems(acct.Plan, req.Kind, req.Items)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	store, ok := s.edgeRuleListStore(w)
	if !ok {
		return
	}
	maxLists := api.MustLimitsFor(acct.Plan).EdgeRuleListsPerAccount
	l, err := store.CreateEdgeRuleList(r.Context(), state.CreateEdgeRuleListParams{
		AccountID: acct.ID, OrgID: listScopeFor(r).orgID, Name: req.Name, Kind: req.Kind, Description: req.Description, Items: items,
	}, maxLists)
	switch {
	case errors.Is(err, state.ErrEdgeRuleListQuota):
		existing, _ := store.ListEdgeRuleLists(r.Context(), acct.ID)
		scope, observed := listScopeFor(r), 0
		for _, l := range existing {
			if l.OrgID == scope.orgID {
				observed++
			}
		}
		api.WriteProblem(w, api.ErrPlanLimitEdgeRuleLists(acct.Plan, maxLists, observed))
		return
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.ErrEdgeRuleListExists(req.Name))
		return
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("could not create edge rule list"))
		return
	}
	s.Host.EmitAudit(r.Context(), "edge_rule_list.created", &acct.ID, map[string]any{
		"list_id": l.ID, "name": l.Name, "kind": l.Kind, "item_count": len(l.Items),
	})
	httpkit.WriteJSON(w, http.StatusCreated, EdgeRuleListResponse(l, nil, true))
}

// editEdgeRuleListItems applies an update's items / add / remove to the
// current items.
func editEdgeRuleListItems(current []string, req api.UpdateEdgeRuleListRequest, kind string) ([]string, *api.Problem) {
	if req.Items != nil && (len(req.Add) > 0 || len(req.Remove) > 0) {
		return nil, api.ErrValidation("items replaces the list and cannot be combined with add or remove")
	}
	if req.Items != nil {
		return *req.Items, nil
	}
	remove, err := api.NormalizeEdgeRuleListItems(kind, req.Remove)
	if err != nil {
		return nil, api.ErrValidation("remove: " + err.Error())
	}
	drop := make(map[string]struct{}, len(remove))
	for _, v := range remove {
		drop[v] = struct{}{}
	}
	added, err := api.NormalizeEdgeRuleListItems(kind, req.Add)
	if err != nil {
		return nil, api.ErrValidation("add: " + err.Error())
	}
	out := make([]string, 0, len(current)+len(added))
	for _, v := range append(append([]string{}, current...), added...) {
		if _, gone := drop[v]; !gone {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out, nil
}

// PATCH /v1/edge-rule-lists/{name}
func (s *Service) UpdateEdgeRuleList(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.UpdateEdgeRuleListRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	store, ok := s.edgeRuleListStore(w)
	if !ok {
		return
	}
	name := r.PathValue("name")
	params, prob := s.edgeRuleListUpdateParams(r.Context(), store, listScopeFor(r), acct, name, req)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	edit := ReferencedEdit{Operation: "list_updated", Subject: "edge rule list", Hosts: func(ctx context.Context) (map[string][]string, error) {
		return s.edgeRuleListHosts(ctx, store, acct.ID, name)
	}}
	if prob := s.AuthorizeReferencedEdit(r, acct, edit); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	convergences, release, prob := s.PrepareReferencedEdit(r.Context(), edit)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	defer release(context.WithoutCancel(r.Context()))
	l, refs, err := store.UpdateEdgeRuleList(r.Context(), acct.ID, name, params)
	if err != nil {
		AbortEdgeRuleConvergences(r.Context(), convergences)
	}
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.ErrEdgeRuleListNotFound(name))
		return
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("could not update edge rule list"))
		return
	}
	s.Host.EmitAudit(r.Context(), "edge_rule_list.updated", &acct.ID, map[string]any{
		"list_id": l.ID, "name": l.Name, "item_count": len(l.Items), "rules_touched": len(refs),
	})
	if prob := s.ApplyReferencedEdit(r.Context(), w, edit, l.ID, convergences); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, EdgeRuleListResponse(l, refs, true))
}

// edgeRuleListHosts maps each app with a rule referencing the list to those
// rules' match hosts.
func (s *Service) edgeRuleListHosts(ctx context.Context, store state.EdgeRuleListStore, accountID, name string) (map[string][]string, error) {
	refs, err := store.EdgeRuleListReferences(ctx, accountID)
	if err != nil {
		return nil, err
	}
	hosts := map[string][]string{}
	for _, ref := range refs[name] {
		rule, err := s.Host.Store().GetEdgeRuleByID(ctx, ref.RuleID)
		if errors.Is(err, state.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		hosts[ref.AppID] = append(hosts[ref.AppID], rule.MatchHost)
	}
	return hosts, nil
}

// edgeRuleListUpdateParams validates a PATCH body against the stored list.
// Add / remove read the current items first; a concurrent edit between the
// read and the write is last-writer-wins, as for every list field.
func (s *Service) edgeRuleListUpdateParams(ctx context.Context, store state.EdgeRuleListStore, scope listScope, acct state.Account, name string, req api.UpdateEdgeRuleListRequest) (state.UpdateEdgeRuleListParams, *api.Problem) {
	var params state.UpdateEdgeRuleListParams
	if req.Description != nil {
		if prob := validateEdgeRuleListDescription(*req.Description); prob != nil {
			return params, prob
		}
		params.Description = req.Description
	}
	list, _, prob := s.scopedEdgeRuleList(ctx, store, scope, acct.ID, name)
	if prob != nil {
		return params, prob
	}
	if req.Items == nil && len(req.Add) == 0 && len(req.Remove) == 0 {
		return params, nil
	}
	edited, prob := editEdgeRuleListItems(list.Items, req, list.Kind)
	if prob != nil {
		return params, prob
	}
	items, prob := validateEdgeRuleListItems(acct.Plan, list.Kind, edited)
	if prob != nil {
		return params, prob
	}
	params.Items = &items
	return params, nil
}

// DELETE /v1/edge-rule-lists/{name}
func (s *Service) DeleteEdgeRuleList(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.edgeRuleListStore(w)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if _, _, prob := s.scopedEdgeRuleList(r.Context(), store, listScopeFor(r), acct.ID, name); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	err := store.DeleteEdgeRuleList(r.Context(), acct.ID, name)
	var inUse *state.EdgeRuleListInUseError
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.ErrEdgeRuleListNotFound(name))
		return
	case errors.As(err, &inUse):
		ids := make([]string, len(inUse.Refs))
		for i, ref := range inUse.Refs {
			ids[i] = ref.RuleID
		}
		api.WriteProblem(w, api.ErrEdgeRuleListInUse(name, ids))
		return
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("could not delete edge rule list"))
		return
	}
	s.Host.EmitAudit(r.Context(), "edge_rule_list.deleted", &acct.ID, map[string]any{"name": name})
	w.WriteHeader(http.StatusNoContent)
}
