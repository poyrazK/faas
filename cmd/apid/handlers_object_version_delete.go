package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
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
	if len(q) != 2 || len(q["key"]) != 1 || len(q["version_id"]) != 1 || !state.ValidObjectVersionID(q.Get("version_id")) {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	s.deleteSelectedBucketObjectVersion(w, r, b, provider, q.Get("key"), q.Get("version_id"))
}

func (s *server) deleteSelectedBucketObjectVersion(w http.ResponseWriter, r *http.Request, b state.ObjectBucket, p objectstorage.Provider, key, selector string) {
	id, e := controlDeletionID(r)
	if e != nil {
		bucketProblem(w, e)
		return
	}
	w.Header().Set("X-Gregale-Delete-Id", id)
	j, e := s.deleteMutableBucketObject(r.Context(), b, p, key, selector, id)
	if e != nil {
		bucketProblem(w, e)
		return
	}
	writeJSON(w, http.StatusOK, api.ObjectVersionDeleteResult{VersionID: j.VersionID, DeleteMarker: j.DeleteMarker})
}
