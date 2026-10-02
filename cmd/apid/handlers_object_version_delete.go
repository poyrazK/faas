package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) deleteBucketObjectVersion(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	b, _, provider, ok := s.loadBucket(w, r, acct, true)
	if !ok || !s.authorizeBucketData(w, r, b, state.ObjectBucketPermissionWrite) {
		return
	}
	if err := objectstorage.ValidateObjectDeleteRequest(r); err != nil {
		bucketProblem(w, err)
		return
	}
	q := r.URL.Query()
	if len(q) != 2 || len(q["key"]) != 1 || len(q["version_id"]) != 1 {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	st, _ := s.store.(state.ObjectVersionReferenceStore)
	metrics, ok := s.store.(state.ObjectStorageProviderUsageStore)
	if !ok {
		bucketProblem(w, objectstorage.ErrConfiguration)
		return
	}
	result, err := objectstorage.DeleteOwnedObjectVersion(r.Context(), st, provider, b, q.Get("key"), q.Get("version_id"), objectstorage.VersioningRequestRecorder(metrics, b.ID))
	if err != nil {
		bucketProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
