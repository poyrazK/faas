// adr: 649
package main

import (
	"context"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) reuseJobOperationUpload(w http.ResponseWriter, r *http.Request) {
	store, authority, ok := s.jobArtifactRuntime(w, r)
	if !ok {
		return
	}
	var req api.OperationArtifactUploadRequest
	if !decodeOperationBody(w, r, &req, api.OperationReportBodyMaxBytes) {
		return
	}
	declaration := operations.JobUploadArtifactDeclaration(r.PathValue("id"), authority.RunID, authority.Attempt, req)
	response, err := store.ReuseJobOperationArtifact(r.Context(), r.PathValue("id"), authority, declaration)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func operationUploadDeclaration(r *http.Request) (api.OperationArtifactUploadRequest, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) != 4 {
		return api.OperationArtifactUploadRequest{}, state.ErrInvalidArgument
	}
	for _, key := range []string{"report_id", "name", "size_bytes", "sha256"} {
		if len(query[key]) != 1 {
			return api.OperationArtifactUploadRequest{}, state.ErrInvalidArgument
		}
	}
	size, err := strconv.ParseInt(query.Get("size_bytes"), 10, 64)
	if err != nil || size < 0 || strconv.FormatInt(size, 10) != query.Get("size_bytes") {
		return api.OperationArtifactUploadRequest{}, state.ErrInvalidArgument
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/octet-stream" {
		return api.OperationArtifactUploadRequest{}, state.ErrInvalidArgument
	}
	return api.OperationArtifactUploadRequest{ReportID: query.Get("report_id"), Name: query.Get("name"), SizeBytes: size, SHA256: query.Get("sha256")}, nil
}

func (s *server) uploadJobOperationArtifact(w http.ResponseWriter, r *http.Request) {
	store, authority, ok := s.jobArtifactRuntime(w, r)
	if !ok {
		return
	}
	req, err := operationUploadDeclaration(r)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	ctx, cancel := operationUploadContext(r.Context(), w, r)
	defer cancel()
	response, err := s.retainJobOperationUpload(ctx, store, r.PathValue("id"), authority, req, r.Body)
	if err != nil {
		writeOperationArtifactError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) retainJobOperationUpload(ctx context.Context, store state.OperationJobArtifactStore, id string, authority state.JobOperationAuthority, req api.OperationArtifactUploadRequest, body io.ReadCloser) (api.OperationJobArtifactResponse, error) {
	declaration := operations.JobUploadArtifactDeclaration(id, authority.RunID, authority.Attempt, req)
	response, err := store.ReuseJobOperationArtifact(ctx, id, authority, declaration)
	if err != nil || response.Available {
		return response, err
	}
	if s.operationArtifactStorage == nil {
		return response, objectstorage.ErrUnavailable
	}
	blob, op, err := store.ReserveJobOperationArtifact(ctx, id, authority, declaration)
	if err != nil {
		return response, err
	}
	if blob.State != "retained" {
		file, err := s.spoolValidatedOperationArtifact(op, declaration, func() (io.ReadCloser, error) { return body, nil })
		if err != nil {
			return response, err
		}
		defer func() { _ = file.Close() }()
		// The reservation may have expired while receiving bytes. Native
		// authority is rechecked again when the verified file is committed.
		if !blob.ExpiresAt.After(time.Now()) {
			return response, state.ErrOperationStaleAttempt
		}
		response, err = store.ReuseJobOperationArtifact(ctx, id, authority, declaration)
		if err != nil || response.Available {
			return response, err
		}
		if err := s.operationArtifactStorage.Put(ctx, blob.StorageKey, file); err != nil {
			return response, err
		}
	}
	return store.PrepareVerifiedJobOperationArtifact(ctx, id, authority, declaration, blob.ID)
}
