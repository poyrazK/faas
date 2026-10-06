package main

import (
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) objectLockBucket(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ObjectBucket, objectstorage.Backend, state.ObjectBucketObjectLockStore, bool) {
	w.Header().Set("Cache-Control", "no-store")
	b, _, _, ok := s.loadBucket(w, r, acct, true)
	if !ok || !s.authorizeBucketData(w, r, b, state.ObjectBucketPermissionWrite) {
		return b, objectstorage.Backend{}, nil, false
	}
	st, supported := s.store.(state.ObjectBucketObjectLockStore)
	if !supported {
		bucketProblem(w, objectstorage.ErrUnsupported)
		return b, objectstorage.Backend{}, nil, false
	}
	if r.URL.RawQuery != "" {
		bucketProblem(w, objectstorage.ErrInvalid)
		return b, objectstorage.Backend{}, nil, false
	}
	backend, err := s.objectStorage.Resolve(b.BackendID, b.BackendFingerprint)
	if err != nil {
		bucketProblem(w, err)
		return b, backend, st, false
	}
	return b, backend, st, true
}

func readObjectLockControlBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		return nil, objectstorage.ErrInvalid
	}
	return body, nil
}

func objectLockProgressExists(j state.ObjectBucketObjectLock) bool {
	return j.Revision > 0 || j.EnabledRequired || j.ObservedKnown || j.State != "ready"
}

func (s *server) getObjectBucketObjectLock(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, backend, st, ok := s.objectLockBucket(w, r, acct)
	if !ok {
		return
	}
	if _, err := readObjectLockControlBody(w, r, 0); err != nil {
		bucketProblem(w, err)
		return
	}
	j, err := st.GetObjectBucketObjectLock(r.Context(), acct.ID, b.AppID, b.ID)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	if !backend.ObjectLock.PublicCapabilities(backend.Provider).BucketConfiguration {
		if !objectLockProgressExists(j) {
			bucketProblem(w, objectstorage.ErrUnsupported)
			return
		}
		writeJSON(w, http.StatusOK, state.ViewObjectBucketObjectLock(j))
		return
	}
	svc, err := s.objectLockService(b, backend, st)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	j, _, err = svc.Read(r.Context(), b)
	if err != nil {
		latest, storedErr := st.GetObjectBucketObjectLock(r.Context(), acct.ID, b.AppID, b.ID)
		if storedErr != nil || latest.State == "ready" {
			bucketProblem(w, err)
			return
		}
		j = latest
	}
	writeJSON(w, http.StatusOK, state.ViewObjectBucketObjectLock(j))
}

func (s *server) putObjectBucketObjectLock(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.objectStorageEnabled() {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return
	}
	b, backend, st, ok := s.objectLockBucket(w, r, acct)
	if !ok {
		return
	}
	body, err := readObjectLockControlBody(w, r, api.MaxObjectLockBodyBytes)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	configuration, err := objectstorage.DecodeObjectBucketObjectLockRequest(body)
	if err == nil {
		err = backend.ObjectLock.ValidateConfiguration(backend.Provider, configuration)
	}
	if err != nil {
		bucketProblem(w, err)
		return
	}
	svc, err := s.objectLockService(b, backend, st)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	j, err := svc.Request(r.Context(), b, configuration)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "object_storage.object_lock_requested", &acct.ID, map[string]any{"bucket_id": b.ID, "revision": j.Revision, "has_default": configuration.DefaultRetention != nil})
	writeJSON(w, http.StatusAccepted, state.ViewObjectBucketObjectLock(j))
}

func (s *server) getObjectBucketObjectLockCapabilities(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, backend, _, ok := s.objectLockBucket(w, r, acct)
	if !ok {
		return
	}
	if _, err := readObjectLockControlBody(w, r, 0); err != nil {
		bucketProblem(w, err)
		return
	}
	if b.State != "ready" {
		bucketProblem(w, state.ErrConflict)
		return
	}
	writeJSON(w, http.StatusOK, backend.ObjectLock.PublicCapabilities(backend.Provider))
}

func (s *server) objectLockService(b state.ObjectBucket, backend objectstorage.Backend, st state.ObjectBucketObjectLockStore) (objectstorage.BucketObjectLockService, error) {
	metrics, measured := s.store.(state.ObjectStorageProviderUsageStore)
	native, supported := backend.Provider.(objectstorage.BucketObjectLockProvider)
	versioning, versioned := backend.Provider.(objectstorage.BucketVersioningProvider)
	if !measured || !supported || !versioned || !objectstorage.SupportsNativeObjectLock(backend.Provider) {
		return objectstorage.BucketObjectLockService{}, objectstorage.ErrUnsupported
	}
	return objectstorage.BucketObjectLockService{Store: st, Provider: native, Versioning: versioning, BeforeRequest: objectstorage.VersioningRequestRecorder(metrics, b.ID)}, nil
}
