package main

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func viewObjectS3CopySource(g state.ObjectS3CopySource) api.ObjectS3CopySource {
	return api.ObjectS3CopySource{SourceBucketID: g.SourceBucketID, Prefix: g.Prefix, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt}
}
func (s *server) copySourceManagement(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ObjectBucket, state.ObjectS3CopySourceStore, bool) {
	w.Header().Set("Cache-Control", "no-store")
	b, credentials, ok := s.objectS3CredentialStore(w, r, acct, false)
	if !ok || !s.authorizeBucketData(w, r, b, state.ObjectBucketPermissionWrite) {
		return b, nil, false
	}
	id, err := uuid.Parse(r.PathValue("credential"))
	if err != nil || id == uuid.Nil || id.String() != r.PathValue("credential") {
		bucketProblem(w, state.ErrNotFound)
		return b, nil, false
	}
	owned, supported := credentials.(state.ObjectS3CredentialBindingStore)
	if !supported {
		bucketProblem(w, objectstorage.ErrUnsupported)
		return b, nil, false
	}
	if _, err = owned.GetObjectS3Credential(r.Context(), acct.ID, b.ID, id.String()); err != nil {
		bucketProblem(w, err)
		return b, nil, false
	}
	sources, ok := s.store.(state.ObjectS3CopySourceStore)
	if !ok {
		bucketProblem(w, objectstorage.ErrUnsupported)
		return b, nil, false
	}
	if r.URL.RawQuery != "" {
		bucketProblem(w, objectstorage.ErrInvalid)
		return b, nil, false
	}
	return b, sources, true
}
func (s *server) listObjectS3CopySources(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, sources, ok := s.copySourceManagement(w, r, acct)
	if !ok {
		return
	}
	if r.ContentLength != 0 {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	grants, err := sources.ListObjectS3CopySources(r.Context(), acct.ID, b.ID, r.PathValue("credential"))
	if err != nil {
		bucketProblem(w, err)
		return
	}
	out := api.ObjectS3CopySourceList{Items: make([]api.ObjectS3CopySource, 0, len(grants))}
	for _, g := range grants {
		out.Items = append(out.Items, viewObjectS3CopySource(g))
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *server) setObjectS3CopySource(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.objectStorageEnabled() {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return
	}
	b, sources, ok := s.copySourceManagement(w, r, acct)
	if !ok {
		return
	}
	source, ok := s.authorizeCopySourceManagement(w, r, acct, b, sources)
	if !ok {
		return
	}
	var in api.SetObjectS3CopySourceRequest
	if !decodeBucketRequest(w, r, &in) {
		return
	}
	if len(in.Prefix) > api.MaxObjectCopySourcePrefixBytes || !utf8.ValidString(in.Prefix) || strings.ContainsAny(in.Prefix, "\x00\r\n") {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	g, err := sources.SetObjectS3CopySource(r.Context(), acct.ID, b.ID, r.PathValue("credential"), source.ID, in.Prefix)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "object_storage.copy_source_granted", &acct.ID, map[string]any{"bucket_id": b.ID, "credential_id": g.CredentialID, "source_bucket_id": source.ID})
	writeJSON(w, http.StatusOK, viewObjectS3CopySource(g))
}
func (s *server) authorizeCopySourceManagement(w http.ResponseWriter, r *http.Request, acct state.Account, destination state.ObjectBucket, sources state.ObjectS3CopySourceStore) (state.ObjectBucket, bool) {
	id, err := uuid.Parse(r.PathValue("source"))
	if err != nil || id == uuid.Nil || id.String() != r.PathValue("source") {
		bucketProblem(w, state.ErrNotFound)
		return state.ObjectBucket{}, false
	}
	source, err := sources.GetObjectS3CopySourceBucket(r.Context(), acct.ID, id.String())
	if err != nil {
		bucketProblem(w, err)
		return source, false
	}
	if !s.authorizeBucketData(w, r, source, state.ObjectBucketPermissionRead) {
		return source, false
	}
	if source.ID == destination.ID || source.State != "ready" || destination.State != "ready" {
		bucketProblem(w, state.ErrConflict)
		return source, false
	}
	if source.BackendID != destination.BackendID || source.BackendFingerprint != destination.BackendFingerprint {
		bucketProblem(w, objectstorage.ErrUnsupported)
		return source, false
	}
	if s.objectStorage == nil {
		bucketProblem(w, objectstorage.ErrUnavailable)
		return source, false
	}
	backend, err := s.objectStorage.Resolve(destination.BackendID, destination.BackendFingerprint)
	if err != nil {
		bucketProblem(w, err)
		return source, false
	}
	if _, ok := backend.Provider.(objectstorage.CrossBucketTrackedObjectCopier); !ok {
		bucketProblem(w, objectstorage.ErrUnsupported)
		return source, false
	}
	if _, ok := backend.Provider.(objectstorage.CrossBucketMultipartPartCopier); !ok {
		bucketProblem(w, objectstorage.ErrUnsupported)
		return source, false
	}
	return source, true
}
func (s *server) deleteObjectS3CopySource(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, sources, ok := s.copySourceManagement(w, r, acct)
	if !ok {
		return
	}
	if r.ContentLength != 0 {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	id, err := uuid.Parse(r.PathValue("source"))
	if err != nil || id == uuid.Nil || id.String() != r.PathValue("source") {
		bucketProblem(w, state.ErrNotFound)
		return
	}
	if err = sources.DeleteObjectS3CopySource(r.Context(), acct.ID, b.ID, r.PathValue("credential"), id.String()); err != nil {
		bucketProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "object_storage.copy_source_revoked", &acct.ID, map[string]any{"bucket_id": b.ID, "credential_id": r.PathValue("credential"), "source_bucket_id": id.String()})
	w.WriteHeader(http.StatusNoContent)
}
