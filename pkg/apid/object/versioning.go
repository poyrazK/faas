package object

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) bucketVersioningService(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ObjectBucket, objectstorage.BucketVersioningService, bool) {
	b, _, p, ok := s.loadBucket(w, r, acct, true)
	if !ok {
		return b, objectstorage.BucketVersioningService{}, false
	}
	if !s.AuthorizeBucketData(w, r, b, state.ObjectBucketPermissionWrite) {
		return b, objectstorage.BucketVersioningService{}, false
	}
	st, ok := s.Host.Store().(state.ObjectBucketVersioningStore)
	provider, supported := p.(objectstorage.BucketVersioningProvider)
	_, measured := s.Host.Store().(state.ObjectStorageProviderUsageStore)
	if !ok || !supported || !measured {
		BucketProblem(w, objectstorage.ErrUnsupported)
		return b, objectstorage.BucketVersioningService{}, false
	}
	w.Header().Set("Cache-Control", "no-store")
	return b, objectstorage.BucketVersioningService{Store: st, Provider: provider, BeforeRequest: s.CustomerObjectRequestRecorder(b)}, true
}
func (s *Service) GetObjectBucketVersioning(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, svc, ok := s.bucketVersioningService(w, r, acct)
	if !ok {
		return
	}
	j, _, err := svc.Read(r.Context(), b)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, j.ObjectBucketVersioning)
}
func (s *Service) PutObjectBucketVersioning(w http.ResponseWriter, r *http.Request, acct state.Account) {
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
		BucketProblem(w, err)
		return
	}
	s.Host.EmitAudit(r.Context(), "object_storage.versioning_requested", &acct.ID, map[string]any{"bucket_id": b.ID, "status": j.DesiredStatus, "revision": j.Revision})
	httpkit.WriteJSON(w, http.StatusAccepted, j.ObjectBucketVersioning)
}
