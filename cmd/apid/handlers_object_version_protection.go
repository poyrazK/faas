package main

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) versionProtectionService(b state.ObjectBucket, p objectstorage.Provider) objectstorage.VersionProtectionService {
	st, _ := s.store.(state.ObjectVersionProtectionStore)
	refs, _ := s.store.(state.ObjectVersionReferenceStore)
	lock, _ := s.store.(state.ObjectBucketObjectLockStore)
	metrics, _ := s.store.(state.ObjectStorageProviderUsageStore)
	return objectstorage.VersionProtectionService{Store: st, References: refs, BucketLock: lock, Provider: p, BeforeRequest: objectstorage.VersioningRequestRecorder(metrics, b.ID)}
}
func (s *server) objectVersionRetention(w http.ResponseWriter, r *http.Request, acct state.Account) {
	r.SetPathValue("protection", "retention")
	s.objectVersionProtection(w, r, acct)
}
func (s *server) objectVersionLegalHold(w http.ResponseWriter, r *http.Request, acct state.Account) {
	r.SetPathValue("protection", "legal-hold")
	s.objectVersionProtection(w, r, acct)
}
func (s *server) objectVersionProtection(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	b, _, p, ok := s.loadBucket(w, r, acct, true)
	permission := state.ObjectBucketPermissionRead
	if r.Method == http.MethodPut {
		permission = state.ObjectBucketPermissionWrite
	}
	if !ok || !s.authorizeBucketData(w, r, b, permission) {
		return
	}
	key, version, kind, err := versionProtectionSelection(r)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	svc := s.versionProtectionService(b, p)
	if r.Method == http.MethodGet {
		s.getVersionProtection(w, r, b, svc, key, version, kind)
		return
	}
	backend, err := s.objectStorage.Resolve(b.BackendID, b.BackendFingerprint)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	if !s.objectStorageEnabled() || !backend.ObjectLock.PublicCapabilities(p).VersionRetention {
		bucketProblem(w, objectstorage.ErrUnsupported)
		return
	}
	s.putVersionProtection(w, r, b, svc, key, version, kind)
}
func versionProtectionSelection(r *http.Request) (string, string, string, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q) != 2 || len(q["key"]) != 1 || len(q["version_id"]) != 1 || !objectstorage.ValidKey(q.Get("key")) || !state.ValidObjectVersionID(q.Get("version_id")) {
		return "", "", "", objectstorage.ErrInvalid
	}
	kind := r.PathValue("protection")
	if kind != "retention" && kind != "legal-hold" {
		return "", "", "", objectstorage.ErrInvalid
	}
	if kind == "legal-hold" {
		kind = "legal_hold"
	}
	return q.Get("key"), q.Get("version_id"), kind, nil
}
func (s *server) getVersionProtection(w http.ResponseWriter, r *http.Request, b state.ObjectBucket, svc objectstorage.VersionProtectionService, key, version, kind string) {
	if _, err := readObjectLockControlBody(w, r, 0); err != nil {
		bucketProblem(w, err)
		return
	}
	retention, hold, err := svc.Read(r.Context(), b, key, version, kind)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	if kind == "retention" {
		writeJSON(w, 200, api.ObjectVersionRetentionResult{VersionID: version, Retention: retention})
	} else {
		writeJSON(w, 200, api.ObjectVersionLegalHoldResult{VersionID: version, LegalHold: hold})
	}
}
func (s *server) putVersionProtection(w http.ResponseWriter, r *http.Request, b state.ObjectBucket, svc objectstorage.VersionProtectionService, key, version, kind string) {
	body, err := readObjectLockControlBody(w, r, api.MaxObjectLockBodyBytes)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	retention, hold, err := objectstorage.DecodeObjectVersionProtectionRequest(body, kind)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	j := state.ObjectVersionProtection{ObjectVersionProtection: api.ObjectVersionProtection{Key: key, VersionID: version, Kind: kind}}
	if kind == "retention" {
		j.ID = retention.ID
		j.Retention = &retention.Retention
	} else {
		j.ID = hold.ID
		j.LegalHold = &hold.LegalHold
	}
	j, err = svc.Request(r.Context(), b, j)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	// Acceptance is durable. The bounded worker owns provider reconciliation.
	status := http.StatusAccepted
	if j.State == "ready" || j.State == "failed" {
		status = http.StatusOK
	}
	s.audit.Emit(r.Context(), "object_storage.version_protection_requested", &b.AccountID, map[string]any{"bucket_id": b.ID, "operation_id": j.ID, "kind": j.Kind})
	writeJSON(w, status, state.ViewObjectVersionProtection(j))
}
func (s *server) getVersionProtectionOperation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	b, _, ok := s.loadBucketRecord(w, r, acct)
	if !ok || !s.authorizeBucketData(w, r, b, state.ObjectBucketPermissionRead) {
		return
	}
	if r.URL.RawQuery != "" {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	if _, err := readObjectLockControlBody(w, r, 0); err != nil {
		bucketProblem(w, err)
		return
	}
	st, ok := s.store.(state.ObjectVersionProtectionStore)
	if !ok {
		bucketProblem(w, objectstorage.ErrUnsupported)
		return
	}
	j, err := st.GetObjectVersionProtection(r.Context(), acct.ID, b.ID, r.PathValue("operation"))
	if err != nil {
		bucketProblem(w, err)
		return
	}
	writeJSON(w, 200, state.ViewObjectVersionProtection(j))
}

// Errors from accepted reconciliation remain visible on their receipt. A
// provider rejection is terminal; uncertainty never discards accepted intent.
func protectionRecoveryIgnored(err error) bool {
	return errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound)
}
