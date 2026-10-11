package main

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) bucketEncryptionService(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ObjectBucket, objectstorage.BucketEncryptionService, bool) {
	w.Header().Set("Cache-Control", "no-store")
	b, _, p, ok := s.loadBucket(w, r, acct, true)
	if !ok || !s.authorizeBucketData(w, r, b, state.ObjectBucketPermissionWrite) {
		return b, objectstorage.BucketEncryptionService{}, false
	}
	st, stored := s.store.(state.ObjectBucketEncryptionStore)
	native, supported := p.(objectstorage.BucketEncryptionProvider)
	_, measured := s.store.(state.ObjectStorageProviderUsageStore)
	if !stored || !supported || !measured {
		bucketProblem(w, objectstorage.ErrUnsupported)
		return b, objectstorage.BucketEncryptionService{}, false
	}
	if r.URL.RawQuery != "" {
		bucketProblem(w, objectstorage.ErrInvalid)
		return b, objectstorage.BucketEncryptionService{}, false
	}
	return b, objectstorage.BucketEncryptionService{Store: st, Provider: native, BeforeRequest: s.customerObjectRequestRecorder(b)}, true
}

func (s *server) getObjectBucketEncryption(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, svc, ok := s.bucketEncryptionService(w, r, acct)
	if !ok {
		return
	}
	if r.ContentLength != 0 {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	j, _, err := svc.Read(r.Context(), b)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state.ViewObjectBucketEncryption(j))
}

func (s *server) putObjectBucketEncryption(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.objectStorageEnabled() {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return
	}
	b, svc, ok := s.bucketEncryptionService(w, r, acct)
	if !ok {
		return
	}
	var in api.ObjectBucketEncryptionRequest
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.MaxObjectBucketEncryptionBodyBytes))
	if d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	snapshot, err := s.resolveObjectURLEncryption(b, objectstorage.SignRequest{Method: http.MethodPut, Encryption: &in.Encryption})
	if err != nil {
		bucketProblem(w, err)
		return
	}
	j, err := svc.Request(r.Context(), b, snapshot)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "object_storage.encryption_requested", &acct.ID, map[string]any{"bucket_id": b.ID, "revision": j.Revision, "algorithm": snapshot.Selection.Algorithm})
	writeJSON(w, http.StatusAccepted, state.ViewObjectBucketEncryption(j))
}

func (s *server) deleteObjectBucketEncryption(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, svc, ok := s.bucketEncryptionService(w, r, acct)
	if !ok {
		return
	}
	if r.ContentLength != 0 {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	j, err := svc.Request(r.Context(), b, state.ObjectEncryptionSnapshot{})
	if err != nil {
		bucketProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "object_storage.encryption_clear_requested", &acct.ID, map[string]any{"bucket_id": b.ID, "revision": j.Revision})
	writeJSON(w, http.StatusAccepted, state.ViewObjectBucketEncryption(j))
}
