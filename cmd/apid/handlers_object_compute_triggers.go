package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) reconcileObjectStorageComputeTriggers(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	b, _, ok := s.loadBucketRecord(w, r, acct)
	if !ok || !s.authorizeBucketData(w, r, b, state.ObjectBucketPermissionWrite) {
		return
	}
	st, ok := s.store.(state.ObjectComputeTriggerStore)
	if !ok {
		bucketProblem(w, objectstorage.ErrUnsupported)
		return
	}
	var request api.ReconcileObjectStorageComputeTriggersRequest
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.MaxObjectNotificationBodyBytes))
	d.DisallowUnknownFields()
	var extra any
	if r.URL.RawQuery != "" || d.Decode(&request) != nil || d.Decode(&extra) != io.EOF || request.ExpectedRevision == nil || request.Triggers == nil {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	if len(request.Triggers) > 0 && !s.objectStorageEnabled() {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return
	}
	out, err := st.ReconcileObjectBucketComputeTriggers(r.Context(), b.AccountID, b.AppID, b.ID, request)
	if errors.Is(err, state.ErrObjectNotificationInvalid) {
		api.WriteProblem(w, api.ErrValidation("invalid compute triggers, filters or destination"))
		return
	}
	if err != nil {
		bucketProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "object_storage.compute_triggers_reconciled", &acct.ID, map[string]any{"bucket_id": b.ID, "revision": out.Revision, "managed_rules": len(out.ManagedRuleIDs)})
	writeJSON(w, http.StatusOK, out)
}
