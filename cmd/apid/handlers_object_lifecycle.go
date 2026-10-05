package main

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) lifecyclePolicyService(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ObjectBucket, objectstorage.LifecyclePolicyService, bool) {
	w.Header().Set("Cache-Control", "no-store")
	b, _, ok := s.loadBucketRecord(w, r, acct)
	if !ok || !s.authorizeBucketData(w, r, b, state.ObjectBucketPermissionWrite) {
		return b, objectstorage.LifecyclePolicyService{}, false
	}
	st, ok := s.store.(state.ObjectLifecycleStore)
	if !ok {
		bucketProblem(w, objectstorage.ErrUnsupported)
		return b, objectstorage.LifecyclePolicyService{}, false
	}
	svc := objectstorage.LifecyclePolicyService{Store: st}
	if r.Method == http.MethodPut {
		if !s.objectStorageEnabled() {
			bucketProblem(w, objectstorage.ErrUnavailable)
			return b, svc, false
		}
		backend, err := s.objectStorage.Resolve(b.BackendID, b.BackendFingerprint)
		if err != nil {
			bucketProblem(w, err)
			return b, svc, false
		}
		svc.Provider = backend.Provider
	}
	return b, svc, true
}
func (s *server) objectBucketLifecycle(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, svc, ok := s.lifecyclePolicyService(w, r, acct)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	if r.Method != http.MethodPut && r.ContentLength != 0 {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	if r.Method == http.MethodGet {
		p, err := svc.Read(r.Context(), b)
		if err != nil {
			bucketProblem(w, err)
			return
		}
		writeJSON(w, http.StatusOK, p.ObjectBucketLifecycle)
		return
	}
	var rules []api.ObjectLifecycleRule
	if r.Method == http.MethodPut {
		var good bool
		rules, good = decodeControlLifecycle(w, r)
		if !good {
			return
		}
	}
	p, err := svc.Write(r.Context(), b, rules)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "object_storage.lifecycle_policy_changed", &acct.ID, map[string]any{"bucket_id": b.ID, "revision": p.Revision, "rules": len(p.Rules)})
	writeJSON(w, http.StatusOK, p.ObjectBucketLifecycle)
}
func decodeControlLifecycle(w http.ResponseWriter, r *http.Request) ([]api.ObjectLifecycleRule, bool) {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.MaxObjectLifecycleBodyBytes))
	d.DisallowUnknownFields()
	var in api.ObjectBucketLifecycleRequest
	var extra any
	if d.Decode(&in) != nil || in.Rules == nil || len(in.Rules) == 0 || d.Decode(&extra) != io.EOF {
		bucketProblem(w, objectstorage.ErrInvalid)
		return nil, false
	}
	return in.Rules, true
}
func (s *server) createObjectLifecycleScan(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, svc, ok := s.lifecyclePolicyService(w, r, acct)
	if !ok {
		return
	}
	if !s.objectStorageEnabled() {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return
	}
	if r.ContentLength != 0 || r.URL.RawQuery != "" {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	j, err := svc.Store.StartObjectLifecycleScan(r.Context(), b.AccountID, b.AppID, b.ID)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, j.ObjectLifecycleScan)
}
func (s *server) getObjectLifecycleScan(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, svc, ok := s.lifecyclePolicyService(w, r, acct)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("scan"))
	if err != nil || id.String() != r.PathValue("scan") {
		bucketProblem(w, state.ErrNotFound)
		return
	}
	if r.URL.RawQuery != "" || r.ContentLength != 0 {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	j, err := svc.Store.GetObjectLifecycleScan(r.Context(), b.AccountID, b.ID, id.String())
	if err != nil {
		bucketProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, j.ObjectLifecycleScan)
}
