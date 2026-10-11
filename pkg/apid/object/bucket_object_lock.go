package object

import (
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) objectLockBucket(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ObjectBucket, objectstorage.Backend, state.ObjectBucketObjectLockStore, bool) {
	w.Header().Set("Cache-Control", "no-store")
	b, _, _, ok := s.loadBucket(w, r, acct, true)
	if !ok || !s.AuthorizeBucketData(w, r, b, state.ObjectBucketPermissionWrite) {
		return b, objectstorage.Backend{}, nil, false
	}
	st, supported := s.Host.Store().(state.ObjectBucketObjectLockStore)
	if !supported {
		BucketProblem(w, objectstorage.ErrUnsupported)
		return b, objectstorage.Backend{}, nil, false
	}
	if r.URL.RawQuery != "" {
		BucketProblem(w, objectstorage.ErrInvalid)
		return b, objectstorage.Backend{}, nil, false
	}
	backend, err := s.Host.ObjectStorage().Resolve(b.BackendID, b.BackendFingerprint)
	if err != nil {
		BucketProblem(w, err)
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

func (s *Service) GetObjectBucketObjectLock(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, backend, st, ok := s.objectLockBucket(w, r, acct)
	if !ok {
		return
	}
	if _, err := readObjectLockControlBody(w, r, 0); err != nil {
		BucketProblem(w, err)
		return
	}
	j, err := st.GetObjectBucketObjectLock(r.Context(), acct.ID, b.AppID, b.ID)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	if !backend.ObjectLock.PublicCapabilities(backend.Provider).BucketConfiguration {
		if !objectLockProgressExists(j) {
			BucketProblem(w, objectstorage.ErrUnsupported)
			return
		}
		httpkit.WriteJSON(w, http.StatusOK, state.ViewObjectBucketObjectLock(j))
		return
	}
	svc, err := s.objectLockService(b, backend, st)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	svc.BeforeRequest = s.CustomerObjectRequestRecorder(b)
	j, _, err = svc.Read(r.Context(), b)
	if err != nil {
		latest, storedErr := st.GetObjectBucketObjectLock(r.Context(), acct.ID, b.AppID, b.ID)
		if storedErr != nil || latest.State == "ready" {
			BucketProblem(w, err)
			return
		}
		j = latest
	}
	httpkit.WriteJSON(w, http.StatusOK, state.ViewObjectBucketObjectLock(j))
}

func (s *Service) PutObjectBucketObjectLock(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.ObjectStorageEnabled() {
		BucketProblem(w, objectstorage.ErrUnavailable)
		return
	}
	b, backend, st, ok := s.objectLockBucket(w, r, acct)
	if !ok {
		return
	}
	body, err := readObjectLockControlBody(w, r, api.MaxObjectLockBodyBytes)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	configuration, err := objectstorage.DecodeObjectBucketObjectLockRequest(body)
	if err == nil {
		err = backend.ObjectLock.ValidateConfiguration(backend.Provider, configuration)
	}
	if err != nil {
		BucketProblem(w, err)
		return
	}
	svc, err := s.objectLockService(b, backend, st)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	svc.BeforeRequest = s.CustomerObjectRequestRecorder(b)
	j, err := svc.Request(r.Context(), b, configuration)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	s.Host.EmitAudit(r.Context(), "object_storage.object_lock_requested", &acct.ID, map[string]any{"bucket_id": b.ID, "revision": j.Revision, "has_default": configuration.DefaultRetention != nil})
	httpkit.WriteJSON(w, http.StatusAccepted, state.ViewObjectBucketObjectLock(j))
}

func (s *Service) GetObjectBucketObjectLockCapabilities(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, backend, _, ok := s.objectLockBucket(w, r, acct)
	if !ok {
		return
	}
	if _, err := readObjectLockControlBody(w, r, 0); err != nil {
		BucketProblem(w, err)
		return
	}
	if b.State != "ready" {
		BucketProblem(w, state.ErrConflict)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, backend.ObjectLock.PublicCapabilities(backend.Provider))
}

func (s *Service) objectLockService(b state.ObjectBucket, backend objectstorage.Backend, st state.ObjectBucketObjectLockStore) (objectstorage.BucketObjectLockService, error) {
	metrics, measured := s.Host.Store().(state.ObjectStorageProviderUsageStore)
	native, supported := backend.Provider.(objectstorage.BucketObjectLockProvider)
	versioning, versioned := backend.Provider.(objectstorage.BucketVersioningProvider)
	if !measured || !supported || !versioned || !objectstorage.SupportsNativeObjectLock(backend.Provider) {
		return objectstorage.BucketObjectLockService{}, objectstorage.ErrUnsupported
	}
	return objectstorage.BucketObjectLockService{Store: st, Provider: native, Versioning: versioning, BeforeRequest: objectstorage.VersioningRequestRecorder(metrics, b.ID)}, nil
}
