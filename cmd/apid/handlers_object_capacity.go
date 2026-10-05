package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) capacityStore(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ObjectBucket, state.ObjectCapacityStore, bool) {
	b, _, ok := s.loadBucketRecord(w, r, acct)
	if !ok {
		return b, nil, false
	}
	if !s.authorizeBucketData(w, r, b, state.ObjectBucketPermissionWrite) {
		return b, nil, false
	}
	st, ok := s.store.(state.ObjectCapacityStore)
	if !ok {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return b, nil, false
	}
	w.Header().Set("Cache-Control", "no-store")
	return b, st, true
}
func (s *server) createObjectCapacityReconciliation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, st, ok := s.capacityStore(w, r, acct)
	if !ok {
		return
	}
	j, err := st.RequestObjectCapacityReconciliation(r.Context(), acct.ID, b.AppID, b.ID)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "object_storage.capacity_reconciliation_requested", &acct.ID, map[string]any{"bucket_id": b.ID, "reconciliation_id": j.ID})
	writeJSON(w, http.StatusAccepted, j.ObjectCapacityReconciliation)
}
func (s *server) getObjectCapacityReconciliation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, st, ok := s.capacityStore(w, r, acct)
	if !ok {
		return
	}
	j, err := st.GetObjectCapacityReconciliation(r.Context(), acct.ID, b.ID, r.PathValue("reconciliation"))
	if err != nil {
		bucketProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, j.ObjectCapacityReconciliation)
}
func (s *server) cancelObjectCapacityReconciliation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, st, ok := s.capacityStore(w, r, acct)
	if !ok {
		return
	}
	j, err := st.CancelObjectCapacityReconciliation(r.Context(), acct.ID, b.ID, r.PathValue("reconciliation"))
	if err != nil {
		bucketProblem(w, err)
		return
	}
	if j.State == "cancelled" {
		s.audit.Emit(r.Context(), "object_storage.capacity_reconciliation_cancelled", &acct.ID, map[string]any{"bucket_id": b.ID, "reconciliation_id": j.ID})
	}
	writeJSON(w, http.StatusOK, j.ObjectCapacityReconciliation)
}
