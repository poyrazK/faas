package main

// ADR-833: reusable edge-rule lists. Account-scoped named sets referenced
// from match conditions with the in_list op.

import (
	"context"
	"errors"
	"net/http"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) edgeRuleListStore(w http.ResponseWriter) (state.EdgeRuleListStore, bool) {
	store, ok := s.store.(state.EdgeRuleListStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("edge rule lists are unavailable"))
	}
	return store, ok
}

// validateEdgeRuleMatchLists resolves a condition's list references against
// the account: an unknown list, or one whose kind does not fit its field, is
// a validation error. Conditions without references need no lookup.
func (s *server) validateEdgeRuleMatchLists(ctx context.Context, accountID string, expr *api.EdgeRuleMatchExpr) *api.Problem {
	names := api.EdgeRuleMatchListRefs(expr)
	if len(names) == 0 {
		return nil
	}
	store, ok := s.store.(state.EdgeRuleListStore)
	if !ok {
		return api.ErrValidation("edge rule lists are unavailable")
	}
	rows, err := store.EdgeRuleListsByName(ctx, accountID, names)
	if err != nil {
		return api.ErrCapacity("could not read edge rule lists")
	}
	lists := make(api.EdgeRuleLists, len(rows))
	for _, l := range rows {
		// Kind is all validation needs; items were validated on write.
		compiled, err := api.CompileEdgeRuleList(l.Kind, nil)
		if err != nil {
			return api.ErrCapacity("stored edge rule list is invalid")
		}
		lists[l.Name] = compiled
	}
	return api.ValidateEdgeRuleMatchWithLists(expr, lists)
}

func edgeRuleListResponse(l state.EdgeRuleList, refs []state.EdgeRuleListRef, withItems bool) api.EdgeRuleListResponse {
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
func (s *server) listEdgeRuleLists(w http.ResponseWriter, r *http.Request, acct state.Account) {
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
	out := api.ListEdgeRuleListsResponse{Lists: make([]api.EdgeRuleListResponse, 0, len(lists))}
	for _, l := range lists {
		out.Lists = append(out.Lists, edgeRuleListResponse(l, refs[l.Name], false))
	}
	writeJSON(w, http.StatusOK, out)
}

// GET /v1/edge-rule-lists/{name}
func (s *server) getEdgeRuleList(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.edgeRuleListStore(w)
	if !ok {
		return
	}
	name := r.PathValue("name")
	lists, err := store.EdgeRuleListsByName(r.Context(), acct.ID, []string{name})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read edge rule lists"))
		return
	}
	if len(lists) == 0 {
		api.WriteProblem(w, api.ErrEdgeRuleListNotFound(name))
		return
	}
	refs, err := store.EdgeRuleListReferences(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read edge rule list references"))
		return
	}
	writeJSON(w, http.StatusOK, edgeRuleListResponse(lists[0], refs[name], true))
}

// validateEdgeRuleListItems normalizes items and enforces the plan's
// per-list item cap.
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
func (s *server) createEdgeRuleList(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateEdgeRuleListRequest
	if err := decodeJSON(r, &req); err != nil {
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
		AccountID: acct.ID, Name: req.Name, Kind: req.Kind, Description: req.Description, Items: items,
	}, maxLists)
	switch {
	case errors.Is(err, state.ErrEdgeRuleListQuota):
		existing, _ := store.ListEdgeRuleLists(r.Context(), acct.ID)
		api.WriteProblem(w, api.ErrPlanLimitEdgeRuleLists(acct.Plan, maxLists, len(existing)))
		return
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.ErrEdgeRuleListExists(req.Name))
		return
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("could not create edge rule list"))
		return
	}
	s.audit.Emit(r.Context(), "edge_rule_list.created", &acct.ID, map[string]any{
		"list_id": l.ID, "name": l.Name, "kind": l.Kind, "item_count": len(l.Items),
	})
	writeJSON(w, http.StatusCreated, edgeRuleListResponse(l, nil, true))
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
func (s *server) updateEdgeRuleList(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.UpdateEdgeRuleListRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	store, ok := s.edgeRuleListStore(w)
	if !ok {
		return
	}
	name := r.PathValue("name")
	params, prob := s.edgeRuleListUpdateParams(r.Context(), store, acct, name, req)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	l, refs, err := store.UpdateEdgeRuleList(r.Context(), acct.ID, name, params)
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.ErrEdgeRuleListNotFound(name))
		return
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("could not update edge rule list"))
		return
	}
	s.audit.Emit(r.Context(), "edge_rule_list.updated", &acct.ID, map[string]any{
		"list_id": l.ID, "name": l.Name, "item_count": len(l.Items), "rules_touched": len(refs),
	})
	writeJSON(w, http.StatusOK, edgeRuleListResponse(l, refs, true))
}

// edgeRuleListUpdateParams validates a PATCH body against the stored list.
// Add / remove read the current items first; a concurrent edit between the
// read and the write is last-writer-wins, as for every list field.
func (s *server) edgeRuleListUpdateParams(ctx context.Context, store state.EdgeRuleListStore, acct state.Account, name string, req api.UpdateEdgeRuleListRequest) (state.UpdateEdgeRuleListParams, *api.Problem) {
	var params state.UpdateEdgeRuleListParams
	if req.Description != nil {
		if prob := validateEdgeRuleListDescription(*req.Description); prob != nil {
			return params, prob
		}
		params.Description = req.Description
	}
	if req.Items == nil && len(req.Add) == 0 && len(req.Remove) == 0 {
		return params, nil
	}
	lists, err := store.EdgeRuleListsByName(ctx, acct.ID, []string{name})
	if err != nil {
		return params, api.ErrCapacity("could not read edge rule lists")
	}
	if len(lists) == 0 {
		return params, api.ErrEdgeRuleListNotFound(name)
	}
	edited, prob := editEdgeRuleListItems(lists[0].Items, req, lists[0].Kind)
	if prob != nil {
		return params, prob
	}
	items, prob := validateEdgeRuleListItems(acct.Plan, lists[0].Kind, edited)
	if prob != nil {
		return params, prob
	}
	params.Items = &items
	return params, nil
}

// DELETE /v1/edge-rule-lists/{name}
func (s *server) deleteEdgeRuleList(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.edgeRuleListStore(w)
	if !ok {
		return
	}
	name := r.PathValue("name")
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
	s.audit.Emit(r.Context(), "edge_rule_list.deleted", &acct.ID, map[string]any{"name": name})
	w.WriteHeader(http.StatusNoContent)
}
