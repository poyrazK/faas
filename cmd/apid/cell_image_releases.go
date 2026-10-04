package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/state"
)

type cellImageReleaseContextKey struct{}
type cellImageReleaseRequest struct {
	intent state.RegionalImageReleaseIntent
	result state.RegionalImageReleaseResult
}

// Durable receipts are authoritative here; the ordinary 24-hour response cache
// is deliberately bypassed so request-body conflicts and tombstones stay visible.
func (s *server) putCellImageRelease(w http.ResponseWriter, r *http.Request, acct state.Account) {
	releaseID := r.PathValue("release_id")
	if !globaldirectory.ValidID(releaseID) {
		api.WriteProblem(w, api.ErrValidation("release_id must be a canonical nonzero UUID"))
		return
	}
	var req api.PutCellImageReleaseRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.CellImageReleaseRequestMaxBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil || decoder.Decode(&struct{}{}) != io.EOF || len(req.Image) > api.CellImageReleaseImageMaxBytes || !api.ValidDeploymentImage(req.Image) || (req.Ordering != nil && !req.Ordering.Valid()) {
		api.WriteProblem(w, api.ErrValidation("send one bounded JSON object containing a digest-pinned image"))
		return
	}
	if s.replayCellImageRelease(w, r, acct, req) {
		return
	}
	app, _ := r.Context().Value(cellDeploymentAppContextKey{}).(state.App)
	request := &cellImageReleaseRequest{intent: state.RegionalImageReleaseIntent{
		CellID: s.controlPlaneCell.ID, AppID: app.ID, ReleaseID: releaseID, AccountID: acct.ID, OrgID: app.OrgID, ImageRef: req.Image,
	}}
	if req.Ordering != nil {
		request.intent.GlobalAppID, request.intent.Generation = req.Ordering.GlobalAppID, req.Ordering.Generation
	}
	r = r.WithContext(context.WithValue(r.Context(), cellImageReleaseContextKey{}, request))
	body, _ := json.Marshal(api.CreateDeploymentRequest{Image: req.Image})
	r.Body, r.ContentLength = io.NopCloser(bytes.NewReader(body)), int64(len(body))
	r.Header.Set("Content-Type", "application/json")
	s.createDeployment(w, r, acct)
}

func (s *server) getCellImageRelease(w http.ResponseWriter, r *http.Request, acct state.Account) {
	receipt, err := s.readCellImageRelease(r, acct)
	if err != nil {
		s.writeCellImageReleaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.cellImageReleaseResponse(receipt, false))
}

func (s *server) readCellImageRelease(r *http.Request, acct state.Account) (state.RegionalImageReleaseReceipt, error) {
	if !globaldirectory.ValidID(r.PathValue("release_id")) {
		return state.RegionalImageReleaseReceipt{}, state.ErrInvalidArgument
	}
	store, ok := s.store.(state.RegionalImageReleaseStore)
	if !ok {
		return state.RegionalImageReleaseReceipt{}, errors.New("regional release receipts unavailable")
	}
	receipt, err := store.RegionalImageReleaseReceipt(r.Context(), r.PathValue("cell_id"), r.PathValue("app_id"), r.PathValue("release_id"))
	if err == nil && receipt.AccountID != acct.ID {
		return state.RegionalImageReleaseReceipt{}, state.ErrNotFound
	}
	return receipt, err
}

func (s *server) replayCellImageRelease(w http.ResponseWriter, r *http.Request, acct state.Account, req api.PutCellImageReleaseRequest) bool {
	receipt, err := s.readCellImageRelease(r, acct)
	if errors.Is(err, state.ErrNotFound) {
		return false
	}
	if err == nil && (receipt.ImageRef != req.Image || !cellImageReleaseOrderingMatches(receipt, req.Ordering)) {
		err = state.ErrRegionalImageReleaseConflict
	}
	if err == nil && receipt.DeploymentID == "" {
		err = state.ErrRegionalImageReleaseExpired
	}
	if err != nil {
		s.writeCellImageReleaseError(w, err)
		return true
	}
	w.Header().Set("Idempotent-Replayed", "true")
	writeJSON(w, http.StatusAccepted, s.cellImageReleaseResponse(receipt, true))
	return true
}

func (s *server) writeCellImageReleaseError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrRegionalImageReleaseConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeRegionalImageReleaseConflict, "Release already accepted", "This release UUID identifies different image or ordering input. Choose a new release UUID."))
	case errors.Is(err, state.ErrRegionalImageReleaseOrder):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeRegionalImageReleaseOrder, "Release ordering conflicts", "This global release generation is stale or the regional app is bound to another release stream."))
	case errors.Is(err, state.ErrRegionalImageReleaseExpired):
		api.WriteProblem(w, api.NewProblem(http.StatusGone, api.CodeRegionalImageReleaseExpired, "Release deployment collected", "The accepted deployment was collected. This release UUID cannot be executed again."))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("invalid regional release identity or image"))
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "no such regional image release")
	default:
		api.WriteProblem(w, api.ErrCapacity("could not access the regional image release receipt"))
	}
}

func (s *server) cellImageReleaseResponse(receipt state.RegionalImageReleaseReceipt, replayed bool) api.CellImageReleaseResponse {
	var deploymentID *string
	if receipt.DeploymentID != "" {
		id := cellImageReleaseWireID(receipt.DeploymentID)
		deploymentID = &id
	}
	response := api.CellImageReleaseResponse{
		CellID: receipt.CellID, Region: s.controlPlaneCell.Region, RegionalAccountID: cellImageReleaseWireID(receipt.AccountID), RegionalAppID: receipt.AppID,
		ReleaseID: receipt.ReleaseID, Image: receipt.ImageRef, DeploymentID: deploymentID, DeploymentStatus: receipt.DeploymentStatus,
		CreatedAt: receipt.CreatedAt, Replayed: replayed,
	}
	if receipt.GlobalAppID != "" {
		response.Ordering = &api.CellImageReleaseOrdering{GlobalAppID: receipt.GlobalAppID, Generation: receipt.Generation}
	}
	return response
}

func cellImageReleaseOrderingMatches(receipt state.RegionalImageReleaseReceipt, ordering *api.CellImageReleaseOrdering) bool {
	if ordering == nil {
		return receipt.GlobalAppID == "" && receipt.Generation == 0
	}
	return receipt.GlobalAppID == ordering.GlobalAppID && receipt.Generation == ordering.Generation
}

func (s *server) createImageDeploymentIntent(r *http.Request, dep state.Deployment, activity *state.OrgActivity) (state.Deployment, int64, bool, error) {
	if request, ok := r.Context().Value(cellImageReleaseContextKey{}).(*cellImageReleaseRequest); ok {
		store, ok := s.store.(state.RegionalImageReleaseStore)
		if !ok {
			return state.Deployment{}, 0, false, errors.New("regional release receipts unavailable")
		}
		result, err := store.CreateRegionalImageRelease(r.Context(), request.intent, dep, activity)
		request.result = result
		return result.Deployment, result.ActivityOutboxID, result.Replayed, err
	}
	if activity != nil {
		if store, ok := s.store.(state.OrgActivityDeploymentMutationStore); ok {
			d, outboxID, err := store.CreateDeploymentWithActivity(r.Context(), dep, *activity)
			return d, outboxID, false, err
		}
	}
	d, err := s.store.CreateDeployment(r.Context(), dep)
	return d, 0, false, err
}

func (s *server) writeImageDeploymentAcceptance(w http.ResponseWriter, r *http.Request, d state.Deployment, app state.App) {
	if request, ok := r.Context().Value(cellImageReleaseContextKey{}).(*cellImageReleaseRequest); ok {
		if request.result.Replayed {
			w.Header().Set("Idempotent-Replayed", "true")
		}
		writeJSON(w, http.StatusAccepted, s.cellImageReleaseResponse(request.result.Receipt, request.result.Replayed))
		return
	}
	writeJSON(w, http.StatusAccepted, s.deploymentResponse(d, app))
}

func cellImageReleaseWireID(raw string) string {
	id, err := uuid.Parse(raw)
	if err != nil {
		return ""
	}
	return id.String()
}
