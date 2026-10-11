// adr: 665
package operation

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) jobArtifactRuntime(w http.ResponseWriter, r *http.Request) (state.OperationJobArtifactStore, state.JobOperationAuthority, bool) {
	_, authority, ok := s.jobOperationRuntime(w, r)
	if !ok {
		return nil, authority, false
	}
	store, ok := s.Host.Store().(state.OperationJobArtifactStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Job operation artifacts unavailable"))
		return nil, authority, false
	}
	return store, authority, true
}

func (s *Service) ReuseJobOperationArtifact(w http.ResponseWriter, r *http.Request) {
	store, authority, ok := s.jobArtifactRuntime(w, r)
	if !ok {
		return
	}
	var req api.OperationArtifactRequest
	if !DecodeOperationBody(w, r, &req, api.OperationReportBodyMaxBytes) {
		return
	}
	response, err := store.ReuseJobOperationArtifact(r.Context(), r.PathValue("id"), authority, req)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, response)
}

func (s *Service) PrepareJobOperationArtifact(w http.ResponseWriter, r *http.Request) {
	store, authority, ok := s.jobArtifactRuntime(w, r)
	if !ok {
		return
	}
	var req api.OperationArtifactRequest
	if !DecodeOperationBody(w, r, &req, api.OperationReportBodyMaxBytes) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.OperationArtifactTransferTimeout)
	defer cancel()
	response, err := s.retainJobOperationArtifact(ctx, store, r.PathValue("id"), authority, req)
	if err != nil {
		writeOperationArtifactError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, response)
}

func (s *Service) retainJobOperationArtifact(ctx context.Context, store state.OperationJobArtifactStore, id string, a state.JobOperationAuthority, req api.OperationArtifactRequest) (api.OperationJobArtifactResponse, error) {
	response, err := store.ReuseJobOperationArtifact(ctx, id, a, req)
	if err != nil || response.Available {
		return response, err
	}
	if s.ArtifactStorage == nil {
		return response, objectstorage.ErrUnavailable
	}
	blob, op, err := store.ReserveJobOperationArtifact(ctx, id, a, req)
	if err != nil {
		return response, err
	}
	if blob.State != "retained" {
		f, err := s.verifyOperationArtifact(ctx, op, req)
		if err != nil {
			return response, err
		}
		defer func() { _ = f.Close() }()
		if !blob.ExpiresAt.After(time.Now()) {
			return response, state.ErrOperationStaleAttempt
		}
		if err := s.ArtifactStorage.Put(ctx, blob.StorageKey, f); err != nil {
			return response, err
		}
	}
	return store.PrepareVerifiedJobOperationArtifact(ctx, id, a, req, blob.ID)
}
