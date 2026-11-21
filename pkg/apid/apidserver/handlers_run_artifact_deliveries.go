package apidserver

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/agents"
	"github.com/onebox-faas/faas/pkg/apid/runartifacts"
	"github.com/onebox-faas/faas/pkg/apid/runfiles"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) runArtifactService(account state.Account) runartifacts.Service {
	s.runArtifactDeliveryOnce.Do(func() { s.runArtifactDeliverySlots = make(chan struct{}, api.RunArtifactDeliveryConcurrency) })
	st, _ := s.store.(runartifacts.Store)
	limits, known := account.Plan.ExecutionLimits()
	enabled := known && limits.Allowed && s.executionAPIEnabled && s.executionV2Enabled && s.object.ObjectStorageEnabled()
	return runartifacts.Service{Store: st, Registry: s.objectStorage, Slots: s.runArtifactDeliverySlots, Enabled: enabled}
}

func (s *server) runArtifactAccess(w http.ResponseWriter, r *http.Request, caller agents.CallbackCaller, permission string) (runartifacts.Access, bool) {
	var app state.App
	if caller.AppID == "" {
		var ok bool
		app, ok = s.loadApp(w, r, caller.Account, r.PathValue("slug"))
		if !ok {
			return runartifacts.Access{}, false
		}
	} else {
		var err error
		app, err = s.store.AppBySlug(r.Context(), r.PathValue("slug"))
		if err != nil || app.ID != caller.AppID || app.AccountID != caller.Account.ID || app.Status == state.AppDeleted {
			s.notFound(w, "no such app")
			return runartifacts.Access{}, false
		}
	}
	access := runartifacts.Access{AccountID: app.AccountID, AppID: app.ID, AppSlug: app.Slug}
	access.AuthorizeBucket = func(_ context.Context, b state.ObjectBucket) error {
		if caller.AppID != "" {
			return nil
		} // Only the verified app's own route/bucket.
		if s.object.Host == nil {
			return api.ErrCapacity("object storage is unavailable")
		}
		if !s.object.AuthorizeBucketData(w, r, b, permission) {
			return errRunArtifactResponseWritten
		}
		return nil
	}
	return access, true
}

func (s *server) deliverRunArtifact(w http.ResponseWriter, r *http.Request, caller agents.CallbackCaller) {
	access, ok := s.runArtifactAccess(w, r, caller, state.ObjectBucketPermissionWrite)
	if !ok {
		return
	}
	var req api.DeliverRunArtifactRequest
	if decodeJSONSized(r, &req, api.RunArtifactDeliveryRequestMaxBytes) != nil {
		api.WriteProblem(w, api.ErrValidation("invalid Run artifact delivery request"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ExecutionFileUploadTimeout)
	defer cancel()
	out, err := s.runArtifactService(caller.Account).Deliver(ctx, access, r.PathValue("route"), req, func(ctx context.Context, req api.DeliverRunArtifactRequest) (io.ReadCloser, error) {
		return s.openRunDeliveryArtifact(ctx, r, caller, req)
	})
	s.writeRunArtifactDelivery(w, out, err)
}

func (s *server) getRunArtifactDelivery(w http.ResponseWriter, r *http.Request, caller agents.CallbackCaller) {
	access, ok := s.runArtifactAccess(w, r, caller, state.ObjectBucketPermissionWrite)
	if !ok {
		return
	}
	out, _, err := s.runArtifactService(caller.Account).Get(r.Context(), access, r.PathValue("route"), r.PathValue("delivery"))
	s.writeRunArtifactDelivery(w, out, err)
}

func (s *server) signRunArtifactDelivery(w http.ResponseWriter, r *http.Request, caller agents.CallbackCaller) {
	access, ok := s.runArtifactAccess(w, r, caller, state.ObjectBucketPermissionRead)
	if !ok {
		return
	}
	_, receipt, err := s.runArtifactService(caller.Account).Get(r.Context(), access, r.PathValue("route"), r.PathValue("delivery"))
	if err != nil {
		s.writeRunArtifactDelivery(w, api.RunArtifactDelivery{}, err)
		return
	}
	if receipt.Status != "completed" {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Delivery not complete", "wait for completed delivery before requesting a download URL"))
		return
	}
	if s.object.Host == nil {
		api.WriteProblem(w, api.ErrCapacity("object storage is unavailable"))
		return
	}
	s.object.SignDeliveredRunArtifact(w, r, caller.Account, receipt)
}

func (s *server) openRunDeliveryArtifact(ctx context.Context, r *http.Request, caller agents.CallbackCaller, req api.DeliverRunArtifactRequest) (io.ReadCloser, error) {
	row, err := s.authorizedRunDeliverySource(ctx, r, caller, req.ExecutionID)
	if err != nil {
		return nil, err
	}
	if row.Status != api.ExecutionStatusSucceeded || row.ResultValidation != nil && row.ResultValidation.Status == "invalid" {
		return nil, api.NewProblem(http.StatusConflict, api.CodeConflict, "Run output unavailable", "delivery requires a successful Run with a valid result contract")
	}
	for _, artifact := range row.Artifacts {
		if artifact.Name != req.ArtifactName {
			continue
		}
		if int64(artifact.SizeBytes) != req.SizeBytes || "sha256:"+strings.TrimPrefix(artifact.SHA256, "sha256:") != req.SHA256 {
			return nil, api.ErrValidation("selected artifact metadata does not match its Run receipt")
		}
		if artifact.FileID == "" {
			return io.NopCloser(bytes.NewReader(artifact.Content)), nil
		}
		file, body, err := s.executionFilesService().Open(ctx, runfiles.Access{Account: caller.Account, Wide: true}, artifact.FileID)
		if err == nil && (file.Kind != "output" || file.ExecutionID == nil || *file.ExecutionID != row.ID || file.Name != artifact.Name || file.SizeBytes != req.SizeBytes || file.SHA256 != req.SHA256) {
			_ = body.Close()
			return nil, api.ErrCapacity("artifact file identity is unavailable")
		}
		return body, err
	}
	return nil, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Artifact not found", "no such output on this Run")
}

func (s *server) authorizedRunDeliverySource(ctx context.Context, r *http.Request, caller agents.CallbackCaller, id string) (state.Execution, error) {
	if caller.AppID != "" {
		if s.agentAPI == nil {
			return state.Execution{}, api.ErrCapacity("Agent task receipts are unavailable")
		}
		row, p := s.agentAPI.TaskDeliveryReceipt(ctx, caller, id)
		if p != nil {
			return row, p
		}
		return row, nil
	}
	access, p := executionAccessForRequest(r)
	if p != nil {
		return state.Execution{}, p
	}
	store, ok := s.store.(state.ExecutionStore)
	if !ok {
		return state.Execution{}, api.ErrCapacity("Run receipts are unavailable")
	}
	row, err := store.ExecutionByID(ctx, caller.Account.ID, id)
	if err != nil || !access.owns(row) {
		return state.Execution{}, state.ErrNotFound
	}
	return row, nil
}
