package object

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// SignDeliveredRunArtifact is called only after authorizing the destination app,
// exact delivery subject and current bucket read grant. Thread grants are no
// longer download authority once bytes belong to this application bucket.
func (s *Service) SignDeliveredRunArtifact(w http.ResponseWriter, r *http.Request, _ state.Account, receipt state.ObjectUploadCompletion) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.ObjectStorageEnabled() {
		BucketProblem(w, ErrObjectStorageDisabled)
		return
	}
	st, ok := s.Host.Store().(state.ObjectBucketStore)
	if !ok {
		BucketProblem(w, objectstorage.ErrUnavailable)
		return
	}
	b, err := st.GetObjectBucket(r.Context(), receipt.AccountID, receipt.AppID, receipt.BucketID)
	if err != nil || b.State != "ready" {
		BucketProblem(w, state.ErrNotFound)
		return
	}
	req := objectstorage.SignRequest{Method: http.MethodGet, Key: receipt.Key, VersionID: receipt.VersionID, ExpiresIn: int64(api.RunArtifactDeliveryDownloadTTL / time.Second)}
	credentials, journal, secret, err := s.prepareObjectURLCredential(r, b, req, state.ObjectBucketPermissionRead)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	urls, ok := s.Host.Store().(state.ObjectURLCapabilityStore)
	if !ok {
		BucketProblem(w, objectstorage.ErrUnavailable)
		return
	}
	credentials, _, err = urls.IssueObjectURLCredential(r.Context(), credentials, journal, s.Host.ObjectStorage().Accounting)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	now := credentials.URL.ExpiresAt.Add(-api.RunArtifactDeliveryDownloadTTL)
	out, err := objectstorage.PresignPublicObject(r.Context(), s.Host.ObjectStorage().PublicEndpoint, s.Host.ObjectStorage().PublicRegion, b.Name, credentials.AccessKeyID, secret, req, now)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}
