// adr: 669
package operation

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) ReuseOperationUpload(w http.ResponseWriter, r *http.Request) {
	op, authority, ok := s.runtimeOperation(w, r)
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.OperationArtifactStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("operation artifact storage unavailable"))
		return
	}
	var req api.OperationArtifactUploadRequest
	if !DecodeOperationBody(w, r, &req, api.OperationReportBodyMaxBytes) {
		return
	}
	declaration := operations.UploadArtifactDeclaration(op.ID, authority.InvocationID, authority.Attempt, req)
	response, err := store.ReuseOperationArtifact(r.Context(), op.ID, authority, declaration)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusOK, response)
}

func (s *Service) UploadOperationArtifact(w http.ResponseWriter, r *http.Request) {
	op, authority, ok := s.runtimeOperation(w, r)
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.OperationArtifactStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("operation artifact storage unavailable"))
		return
	}
	req, err := OperationUploadDeclaration(r)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	ctx, cancel := operationUploadContext(r.Context(), w, r)
	defer cancel()
	// Ownership and instance/JWT validity are checked again after private I/O.
	checkOwner := func() error { _, err := s.runtimeOperationAuthority(r.WithContext(ctx)); return err }
	response, err := s.RetainOperationUpload(ctx, store, op, authority, req, r.Body, checkOwner)
	if err != nil {
		writeOperationArtifactError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusOK, response)
}

func (s *Service) RetainOperationUpload(ctx context.Context, store state.OperationArtifactStore, op state.Operation, a state.OperationExecutionAuthority, req api.OperationArtifactUploadRequest, body io.ReadCloser, checkOwner func() error) (api.OperationArtifactUploadResponse, error) {
	declaration := operations.UploadArtifactDeclaration(op.ID, a.InvocationID, a.Attempt, req)
	response, err := store.ReuseOperationArtifact(ctx, op.ID, a, declaration)
	if err != nil || response.Available {
		return response, err
	}
	blobs, ok := s.Host.Store().(state.OperationResultBlobStore)
	if !ok || s.ArtifactStorage == nil {
		return response, objectstorage.ErrUnavailable
	}
	blob, err := blobs.ReserveOperationArtifact(ctx, op.ID, a, declaration)
	if err != nil {
		return response, err
	}
	if blob.State != "retained" {
		file, err := s.spoolValidatedOperationArtifact(op, declaration, func() (io.ReadCloser, error) { return body, nil })
		if err != nil {
			return response, err
		}
		defer func() { _ = file.Close() }()
		if !blob.ExpiresAt.After(time.Now()) {
			return response, state.ErrOperationStaleAttempt
		}
		if err := checkOwner(); err != nil {
			return response, err
		}
		response, err = store.ReuseOperationArtifact(ctx, op.ID, a, declaration)
		if err != nil || response.Available {
			return response, err
		}
		if err := s.ArtifactStorage.Put(ctx, blob.StorageKey, file); err != nil {
			return response, err
		}
	}
	if err := checkOwner(); err != nil {
		return response, err
	}
	if _, err := store.AttachVerifiedOperationArtifact(ctx, op.ID, a, declaration, blob.ID); err != nil {
		return response, err
	}
	return store.ReuseOperationArtifact(ctx, op.ID, a, declaration)
}
