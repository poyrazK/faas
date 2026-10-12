package object

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) GetObjectBucketEncryptionCapabilities(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	b, _, ok := s.loadBucketRecord(w, r, acct)
	if !ok || !s.AuthorizeBucketData(w, r, b, state.ObjectBucketPermissionWrite) {
		return
	}
	if r.URL.RawQuery != "" || r.ContentLength != 0 {
		BucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	backend, err := s.Host.ObjectStorage().Resolve(b.BackendID, b.BackendFingerprint)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	out := backend.Encryption.PublicCapabilities(acct.ID)
	_, out.BucketDefaults = backend.Provider.(objectstorage.BucketEncryptionProvider)
	httpkit.WriteJSON(w, http.StatusOK, out)
}
