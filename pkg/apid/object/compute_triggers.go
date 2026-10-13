package object

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) ReconcileObjectStorageComputeTriggers(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	b, _, ok := s.loadBucketRecord(w, r, acct)
	if !ok || !s.AuthorizeBucketData(w, r, b, state.ObjectBucketPermissionWrite) {
		return
	}
	st, ok := s.Host.Store().(state.ObjectComputeTriggerStore)
	if !ok {
		BucketProblem(w, objectstorage.ErrUnsupported)
		return
	}
	var request api.ReconcileObjectStorageComputeTriggersRequest
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.MaxObjectNotificationBodyBytes))
	d.DisallowUnknownFields()
	var extra any
	if r.URL.RawQuery != "" || d.Decode(&request) != nil || d.Decode(&extra) != io.EOF || request.ExpectedRevision == nil || request.Triggers == nil {
		BucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	if len(request.Triggers) > 0 && !s.ObjectStorageEnabled() {
		BucketProblem(w, objectstorage.ErrUnavailable)
		return
	}
	out, err := st.ReconcileObjectBucketComputeTriggers(r.Context(), b.AccountID, b.AppID, b.ID, request)
	if errors.Is(err, state.ErrObjectNotificationInvalid) {
		api.WriteProblem(w, api.ErrValidation("invalid compute triggers, filters or destination"))
		return
	}
	if err != nil {
		BucketProblem(w, err)
		return
	}
	s.Host.EmitAudit(r.Context(), "object_storage.compute_triggers_reconciled", &acct.ID, map[string]any{"bucket_id": b.ID, "revision": out.Revision, "managed_rules": len(out.ManagedRuleIDs)})
	httpkit.WriteJSON(w, http.StatusOK, out)
}
