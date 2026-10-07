// adr: 608
package main

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state"
)

// All execution adapters bound receive I/O with the same platform budget.
func operationUploadContext(parent context.Context, w http.ResponseWriter, r *http.Request) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(parent, api.OperationArtifactTransferTimeout)
	deadline, _ := ctx.Deadline()
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = r.Body.Close() })
	return ctx, func() { stop(); cancel(); _ = controller.SetReadDeadline(time.Time{}) }
}

func (s *server) reuseWorkflowOperationUpload(w http.ResponseWriter, r *http.Request) {
	store, authority, ok := s.workflowArtifactRuntime(w, r)
	if !ok {
		return
	}
	var req api.OperationArtifactUploadRequest
	if !decodeOperationBody(w, r, &req, api.OperationReportBodyMaxBytes) {
		return
	}
	declaration := operations.WorkflowUploadArtifactDeclaration(r.PathValue("id"), authority.RunID, authority.StepName, req)
	response, err := store.ReuseWorkflowOperationArtifact(r.Context(), r.PathValue("id"), authority, declaration)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
}

func (s *server) uploadWorkflowOperationArtifact(w http.ResponseWriter, r *http.Request) {
	store, authority, ok := s.workflowArtifactRuntime(w, r)
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
	response, err := s.retainWorkflowOperationUpload(ctx, store, r.PathValue("id"), authority, req, r.Body)
	if err != nil {
		writeOperationArtifactError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
}

func (s *server) retainWorkflowOperationUpload(ctx context.Context, store state.OperationWorkflowArtifactStore, id string, a state.OperationWorkflowAuthority, req api.OperationArtifactUploadRequest, body io.ReadCloser) (api.OperationWorkflowArtifactResponse, error) {
	declaration := operations.WorkflowUploadArtifactDeclaration(id, a.RunID, a.StepName, req)
	response, err := store.ReuseWorkflowOperationArtifact(ctx, id, a, declaration)
	if err != nil || response.Available {
		return response, err
	}
	if s.operationArtifactStorage == nil {
		return response, objectstorage.ErrUnavailable
	}
	blob, op, err := store.ReserveWorkflowOperationArtifact(ctx, id, a, declaration)
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
		// Recheck the native step, owner, cancellation and lease after receiving
		// bytes. Committing rechecks them again after the private storage write.
		response, err = store.ReuseWorkflowOperationArtifact(ctx, id, a, declaration)
		if err != nil || response.Available {
			return response, err
		}
		if err := s.operationArtifactStorage.Put(ctx, blob.StorageKey, file); err != nil {
			return response, err
		}
	}
	return store.PrepareVerifiedWorkflowOperationArtifact(ctx, id, a, declaration, blob.ID)
}
