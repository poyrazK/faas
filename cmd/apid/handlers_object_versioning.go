package main

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) bucketVersioningService(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ObjectBucket, objectstorage.BucketVersioningService, bool) {
	b, _, p, ok := s.loadBucket(w, r, acct, true)
	if !ok {
		return b, objectstorage.BucketVersioningService{}, false
	}
	if !s.authorizeBucketData(w, r, b, state.ObjectBucketPermissionWrite) {
		return b, objectstorage.BucketVersioningService{}, false
	}
	st, ok := s.store.(state.ObjectBucketVersioningStore)
	provider, supported := p.(objectstorage.BucketVersioningProvider)
	metrics, measured := s.store.(state.ObjectStorageProviderUsageStore)
	if !ok || !supported || !measured {
		bucketProblem(w, objectstorage.ErrUnsupported)
		return b, objectstorage.BucketVersioningService{}, false
	}
	w.Header().Set("Cache-Control", "no-store")
	return b, objectstorage.BucketVersioningService{Store: st, Provider: provider, BeforeRequest: objectstorage.VersioningRequestRecorder(metrics, b.ID)}, true
}
func (s *server) getObjectBucketVersioning(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, svc, ok := s.bucketVersioningService(w, r, acct)
	if !ok {
		return
	}
	j, _, err := svc.Read(r.Context(), b)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, j.ObjectBucketVersioning)
}
func (s *server) putObjectBucketVersioning(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, svc, ok := s.bucketVersioningService(w, r, acct)
	if !ok {
		return
	}
	var in api.ObjectBucketVersioningRequest
	if !decodeBucketRequest(w, r, &in) {
		return
	}
	j, err := svc.Request(r.Context(), b, in.Status)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "object_storage.versioning_requested", &acct.ID, map[string]any{"bucket_id": b.ID, "status": j.DesiredStatus, "revision": j.Revision})
	writeJSON(w, http.StatusAccepted, j.ObjectBucketVersioning)
}
