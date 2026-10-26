package object

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func viewObjectS3CopySource(g state.ObjectS3CopySource) api.ObjectS3CopySource {
	return api.ObjectS3CopySource{SourceBucketID: g.SourceBucketID, Prefix: g.Prefix, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt}
}
func (s *Service) copySourceManagement(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ObjectBucket, state.ObjectS3CopySourceStore, bool) {
	w.Header().Set("Cache-Control", "no-store")
	b, credentials, ok := s.objectS3CredentialStore(w, r, acct, false)
	if !ok || !s.AuthorizeBucketData(w, r, b, state.ObjectBucketPermissionWrite) {
		return b, nil, false
	}
	id, err := uuid.Parse(r.PathValue("credential"))
	if err != nil || id == uuid.Nil || id.String() != r.PathValue("credential") {
		BucketProblem(w, state.ErrNotFound)
		return b, nil, false
	}
	owned, supported := credentials.(state.ObjectS3CredentialBindingStore)
	if !supported {
		BucketProblem(w, objectstorage.ErrUnsupported)
		return b, nil, false
	}
	if _, err = owned.GetObjectS3Credential(r.Context(), acct.ID, b.ID, id.String()); err != nil {
		BucketProblem(w, err)
		return b, nil, false
	}
	sources, ok := s.Host.Store().(state.ObjectS3CopySourceStore)
	if !ok {
		BucketProblem(w, objectstorage.ErrUnsupported)
		return b, nil, false
	}
	if r.URL.RawQuery != "" {
		BucketProblem(w, objectstorage.ErrInvalid)
		return b, nil, false
	}
	return b, sources, true
}
func (s *Service) ListObjectS3CopySources(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, sources, ok := s.copySourceManagement(w, r, acct)
	if !ok {
		return
	}
	if r.ContentLength != 0 {
		BucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	grants, err := sources.ListObjectS3CopySources(r.Context(), acct.ID, b.ID, r.PathValue("credential"))
	if err != nil {
		BucketProblem(w, err)
		return
	}
	out := api.ObjectS3CopySourceList{Items: make([]api.ObjectS3CopySource, 0, len(grants))}
	for _, g := range grants {
		out.Items = append(out.Items, viewObjectS3CopySource(g))
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}
func (s *Service) SetObjectS3CopySource(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.ObjectStorageEnabled() {
		BucketProblem(w, ErrObjectStorageDisabled)
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
		BucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	g, err := sources.SetObjectS3CopySource(r.Context(), acct.ID, b.ID, r.PathValue("credential"), source.ID, in.Prefix)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	s.Host.EmitAudit(r.Context(), "object_storage.copy_source_granted", &acct.ID, map[string]any{"bucket_id": b.ID, "credential_id": g.CredentialID, "source_bucket_id": source.ID})
	httpkit.WriteJSON(w, http.StatusOK, viewObjectS3CopySource(g))
}
func (s *Service) authorizeCopySourceManagement(w http.ResponseWriter, r *http.Request, acct state.Account, destination state.ObjectBucket, sources state.ObjectS3CopySourceStore) (state.ObjectBucket, bool) {
	id, err := uuid.Parse(r.PathValue("source"))
	if err != nil || id == uuid.Nil || id.String() != r.PathValue("source") {
		BucketProblem(w, state.ErrNotFound)
		return state.ObjectBucket{}, false
	}
	source, err := sources.GetObjectS3CopySourceBucket(r.Context(), acct.ID, id.String())
	if err != nil {
		BucketProblem(w, err)
		return source, false
	}
	if !s.AuthorizeBucketData(w, r, source, state.ObjectBucketPermissionRead) {
		return source, false
	}
	if source.ID == destination.ID || source.State != "ready" || destination.State != "ready" {
		BucketProblem(w, state.ErrConflict)
		return source, false
	}
	if source.BackendID != destination.BackendID || source.BackendFingerprint != destination.BackendFingerprint {
		BucketProblem(w, objectstorage.ErrUnsupported)
		return source, false
	}
	if s.Host.ObjectStorage() == nil {
		BucketProblem(w, ErrObjectStorageDisabled)
		return source, false
	}
	backend, err := s.Host.ObjectStorage().Resolve(destination.BackendID, destination.BackendFingerprint)
	if err != nil {
		BucketProblem(w, err)
		return source, false
	}
	if _, ok := backend.Provider.(objectstorage.CrossBucketTrackedObjectCopier); !ok {
		BucketProblem(w, objectstorage.ErrUnsupported)
		return source, false
	}
	if _, ok := backend.Provider.(objectstorage.CrossBucketMultipartPartCopier); !ok {
		BucketProblem(w, objectstorage.ErrUnsupported)
		return source, false
	}
	return source, true
}
func (s *Service) DeleteObjectS3CopySource(w http.ResponseWriter, r *http.Request, acct state.Account) {
	b, sources, ok := s.copySourceManagement(w, r, acct)
	if !ok {
		return
	}
	if r.ContentLength != 0 {
		BucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	id, err := uuid.Parse(r.PathValue("source"))
	if err != nil || id == uuid.Nil || id.String() != r.PathValue("source") {
		BucketProblem(w, state.ErrNotFound)
		return
	}
	if err = sources.DeleteObjectS3CopySource(r.Context(), acct.ID, b.ID, r.PathValue("credential"), id.String()); err != nil {
		BucketProblem(w, err)
		return
	}
	s.emitRevocation(r.Context(), "object_storage.copy_source_revoked", acct.ID, map[string]any{"bucket_id": b.ID, "credential_id": r.PathValue("credential"), "source_bucket_id": id.String()})
	w.WriteHeader(http.StatusNoContent)
}
