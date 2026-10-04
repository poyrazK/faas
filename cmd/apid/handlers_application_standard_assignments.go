package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) applicationStandardAssignmentInventoryStore(w http.ResponseWriter, r *http.Request) (state.ApplicationStandardAssignmentInventoryStore, string, bool) {
	_, orgID, ok := s.applicationStandardsStore(w, r, authz.OrgActionViewApplicationStandards)
	if !ok {
		return nil, "", false
	}
	store, ok := s.store.(state.ApplicationStandardAssignmentInventoryStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("application standards assignment inventory is unavailable"))
		return nil, "", false
	}
	return store, orgID, true
}

func (s *server) getApplicationStandardAssignment(w http.ResponseWriter, r *http.Request, _ state.Account) {
	store, orgID, ok := s.applicationStandardAssignmentInventoryStore(w, r)
	if !ok {
		return
	}
	id, ok := standardPathUUID(w, r, "assignment")
	if !ok {
		return
	}
	row, err := store.GetApplicationStandardAssignmentRecord(r.Context(), orgID, id)
	if err != nil {
		writeApplicationStandardMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (s *server) listApplicationStandardAssignments(w http.ResponseWriter, r *http.Request, _ state.Account) {
	store, orgID, ok := s.applicationStandardAssignmentInventoryStore(w, r)
	if !ok {
		return
	}
	after, limit, ok := applicationStandardUUIDPage(w, r)
	if !ok {
		return
	}
	rows, err := store.ListApplicationStandardAssignmentRecords(r.Context(), orgID, after, limit+1)
	if err != nil {
		writeApplicationStandardMutationError(w, err)
		return
	}
	result := api.ApplicationStandardAssignmentList{Assignments: rows}
	if len(rows) > limit {
		result.NextPageAfter = rows[limit-1].ID
		result.Assignments = rows[:limit]
	}
	writeJSON(w, http.StatusOK, result)
}
