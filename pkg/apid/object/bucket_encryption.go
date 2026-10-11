package object

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) bucketEncryptionService(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ObjectBucket, objectstorage.BucketEncryptionService, bool) {
	w.Header().Set("Cache-Control", "no-store")
	b, _, p, ok := s.loadBucket(w, r, acct, true)
	if !ok || !s.AuthorizeBucketData(w, r, b, state.ObjectBucketPermissionWrite) {
		return b, objectstorage.BucketEncryptionService{}, false
	}
	st, stored := s.Host.Store().(state.ObjectBucketEncryptionStore)
	native, supported := p.(objectstorage.BucketEncryptionProvider)
	_, measured := s.Host.Store().(state.ObjectStorageProviderUsageStore)
	if !stored || !supported || !measured {
		BucketProblem(w, objectstorage.ErrUnsupported)
		return b, objectstorage.BucketEncryptionService{}, false
	}
	if r.URL.RawQuery != "" {
		BucketProblem(w, objectstorage.ErrInvalid)
		return b, objectstorage.BucketEncryptionService{}, false
	}
	return b, objectstorage.BucketEncryptionService{Store: st, Provider: native, BeforeRequest: s.CustomerObjectRequestRecorder(b)}, true
}

func (s *Service) GetObjectBucketEncryption(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, svc, ok := s.bucketEncryptionService(w, r, acct)
	if !ok {
		return
	}
	if r.ContentLength != 0 {
		BucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	j, _, err := svc.Read(r.Context(), b)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, state.ViewObjectBucketEncryption(j))
}

func (s *Service) PutObjectBucketEncryption(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.ObjectStorageEnabled() {
		BucketProblem(w, ErrObjectStorageDisabled)
		return
	}
	b, svc, ok := s.bucketEncryptionService(w, r, acct)
	if !ok {
		return
	}
	var in api.ObjectBucketEncryptionRequest
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.MaxObjectBucketEncryptionBodyBytes))
	if d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF {
		BucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	snapshot, err := s.resolveObjectURLEncryption(b, objectstorage.SignRequest{Method: http.MethodPut, Encryption: &in.Encryption})
	if err != nil {
		BucketProblem(w, err)
		return
	}
	j, err := svc.Request(r.Context(), b, snapshot)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	s.Host.EmitAudit(r.Context(), "object_storage.encryption_requested", &acct.ID, map[string]any{"bucket_id": b.ID, "revision": j.Revision, "algorithm": snapshot.Selection.Algorithm})
	httpkit.WriteJSON(w, http.StatusAccepted, state.ViewObjectBucketEncryption(j))
}

func (s *Service) DeleteObjectBucketEncryption(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, svc, ok := s.bucketEncryptionService(w, r, acct)
	if !ok {
		return
	}
	if r.ContentLength != 0 {
		BucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	j, err := svc.Request(r.Context(), b, state.ObjectEncryptionSnapshot{})
	if err != nil {
		BucketProblem(w, err)
		return
	}
	s.Host.EmitAudit(r.Context(), "object_storage.encryption_clear_requested", &acct.ID, map[string]any{"bucket_id": b.ID, "revision": j.Revision})
	httpkit.WriteJSON(w, http.StatusAccepted, state.ViewObjectBucketEncryption(j))
}
