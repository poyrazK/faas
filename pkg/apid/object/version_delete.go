package object

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) DeleteBucketObjectVersion(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	b, _, provider, ok := s.loadBucket(w, r, acct, true)
	if !ok || !s.AuthorizeBucketData(w, r, b, state.ObjectBucketPermissionWrite) {
		return
	}
	if err := objectstorage.ValidateObjectDeleteRequest(r); err != nil {
		BucketProblem(w, err)
		return
	}
	q := r.URL.Query()
	if len(q) != 2 || len(q["key"]) != 1 || len(q["version_id"]) != 1 || !state.ValidObjectVersionID(q.Get("version_id")) {
		BucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	s.deleteSelectedBucketObjectVersion(w, r, b, provider, q.Get("key"), q.Get("version_id"))
}

func (s *Service) deleteSelectedBucketObjectVersion(w http.ResponseWriter, r *http.Request, b state.ObjectBucket, p objectstorage.Provider, key, selector string) {
	id, e := controlDeletionID(r)
	if e != nil {
		BucketProblem(w, e)
		return
	}
	w.Header().Set("X-Gregale-Delete-Id", id)
	j, e := s.DeleteMutableBucketObject(r.Context(), b, p, key, selector, id)
	if e != nil {
		BucketProblem(w, e)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, api.ObjectVersionDeleteResult{VersionID: j.VersionID, DeleteMarker: j.DeleteMarker})
}
