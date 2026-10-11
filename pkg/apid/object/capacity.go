package object

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) capacityStore(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ObjectBucket, state.ObjectCapacityStore, bool) {
	b, _, ok := s.loadBucketRecord(w, r, acct)
	if !ok {
		return b, nil, false
	}
	if !s.AuthorizeBucketData(w, r, b, state.ObjectBucketPermissionWrite) {
		return b, nil, false
	}
	st, ok := s.Host.Store().(state.ObjectCapacityStore)
	if !ok {
		BucketProblem(w, objectstorage.ErrUnavailable)
		return b, nil, false
	}
	w.Header().Set("Cache-Control", "no-store")
	return b, st, true
}
func (s *Service) CreateObjectCapacityReconciliation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, st, ok := s.capacityStore(w, r, acct)
	if !ok {
		return
	}
	j, err := st.RequestObjectCapacityReconciliation(r.Context(), acct.ID, b.AppID, b.ID)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	s.Host.EmitAudit(r.Context(), "object_storage.capacity_reconciliation_requested", &acct.ID, map[string]any{"bucket_id": b.ID, "reconciliation_id": j.ID})
	httpkit.WriteJSON(w, http.StatusAccepted, j.ObjectCapacityReconciliation)
}
func (s *Service) GetObjectCapacityReconciliation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, st, ok := s.capacityStore(w, r, acct)
	if !ok {
		return
	}
	j, err := st.GetObjectCapacityReconciliation(r.Context(), acct.ID, b.ID, r.PathValue("reconciliation"))
	if err != nil {
		BucketProblem(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, j.ObjectCapacityReconciliation)
}
func (s *Service) CancelObjectCapacityReconciliation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, st, ok := s.capacityStore(w, r, acct)
	if !ok {
		return
	}
	j, err := st.CancelObjectCapacityReconciliation(r.Context(), acct.ID, b.ID, r.PathValue("reconciliation"))
	if err != nil {
		BucketProblem(w, err)
		return
	}
	if j.State == "cancelled" {
		s.Host.EmitAudit(r.Context(), "object_storage.capacity_reconciliation_cancelled", &acct.ID, map[string]any{"bucket_id": b.ID, "reconciliation_id": j.ID})
	}
	httpkit.WriteJSON(w, http.StatusOK, j.ObjectCapacityReconciliation)
}
