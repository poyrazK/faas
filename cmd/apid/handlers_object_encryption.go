package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getObjectBucketEncryptionCapabilities(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	b, _, ok := s.loadBucketRecord(w, r, acct)
	if !ok || !s.authorizeBucketData(w, r, b, state.ObjectBucketPermissionWrite) {
		return
	}
	if r.URL.RawQuery != "" || r.ContentLength != 0 {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	backend, err := s.objectStorage.Resolve(b.BackendID, b.BackendFingerprint)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, backend.Encryption.PublicCapabilities(acct.ID))
}
