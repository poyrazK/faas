package main

// adr: 797
import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) cellAccountBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if !globaldirectory.ValidID(s.controlPlaneCell.ID) || s.controlPlaneCell.Region == "" {
		api.WriteProblem(w, api.ErrCapacity("regional cell identity unavailable"))
		return
	}
	writeJSON(w, http.StatusOK, api.CellAccountBindingResponse{CellID: s.controlPlaneCell.ID, Region: s.controlPlaneCell.Region, AccountID: uuid.MustParse(acct.ID).String()})
}

func (s *server) provisionStore(w http.ResponseWriter, r *http.Request) (state.RegionalAppProvisionStore, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if r.PathValue("cell_id") != s.controlPlaneCell.ID || !globaldirectory.ValidID(r.PathValue("provision_id")) {
		api.WriteProblem(w, api.ErrValidation("canonical local cell and provisioning identities required"))
		return nil, false
	}
	store, ok := s.store.(state.RegionalAppProvisionStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("regional app provisioning unavailable"))
	}
	return store, ok
}

func (s *server) getCellAppProvision(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.provisionStore(w, r)
	if !ok {
		return
	}
	p, err := store.RegionalAppProvisionReceipt(r.Context(), s.controlPlaneCell.ID, acct.ID, r.PathValue("provision_id"))
	if err != nil {
		s.appProvisionError(w, err)
		return
	}
	s.writeCellAppProvision(w, r, acct, p, http.StatusOK)
}

func (s *server) putCellAppProvision(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.provisionStore(w, r)
	if !ok {
		return
	}
	var req api.CellAppProvisionRequest
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.GlobalAppProvisionRequestMaxBytes))
	d.DisallowUnknownFields()
	if d.Decode(&req) != nil || d.Decode(&struct{}{}) != io.EOF || !globaldirectory.ValidID(req.GlobalAppID) || req.App.ComputeRegion != s.controlPlaneCell.Region || !globaldirectory.ValidSlug(req.App.Slug) || req.App.OverflowNode != nil {
		api.WriteProblem(w, api.ErrValidation("send bounded immutable app intent for this cell without an overflow node"))
		return
	}
	raw, _ := json.Marshal(req.App)
	if len(raw) > api.GlobalDirectoryRequestMaxBytes {
		api.WriteProblem(w, api.ErrValidation("app creation intent is too large"))
		return
	}
	p := state.RegionalAppProvision{ProvisionID: r.PathValue("provision_id"), GlobalAppID: req.GlobalAppID, CellID: s.controlPlaneCell.ID, Region: s.controlPlaneCell.Region, AccountID: acct.ID, AppSlug: req.App.Slug, Request: raw}
	s.createCellAppProvision(w, r, acct, store, p, req.App)
}

func (s *server) createCellAppProvision(w http.ResponseWriter, r *http.Request, acct state.Account, store state.RegionalAppProvisionStore, p state.RegionalAppProvision, req api.CreateAppRequest) {
	if s.replayAppProvision(w, r, acct, store, p) {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	app, prob := s.buildApp(acct, req, limits)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if prob := s.validateComputeRegionAvailable(r.Context(), req.ComputeRegion); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	entry := newAppLifecycleActivity(r, acct, "app.created", "app.created", map[string]any{"phase": "created", "app_type": string(app.Type), "provision_id": p.ProvisionID})
	p, created, outbox, err := store.CreateRegionalAppProvision(r.Context(), p, app, limits, entry)
	if err != nil {
		s.appProvisionError(w, err)
		return
	}
	if outbox > 0 {
		s.deliverOrgActivityOutbox(r.Context(), outbox)
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
		if actual, err := s.store.AppByID(r.Context(), p.AppID); err == nil {
			s.emitAppCreated(r.Context(), actual)
		}
		s.audit.Emit(r.Context(), "app.provisioned", &acct.ID, map[string]any{"app_id": p.AppID, "provision_id": p.ProvisionID, "global_app_id": p.GlobalAppID, "cell_id": p.CellID})
	}
	s.writeCellAppProvision(w, r, acct, p, status)
}

func (s *server) writeCellAppProvision(w http.ResponseWriter, r *http.Request, acct state.Account, p state.RegionalAppProvision, status int) {
	app, err := s.store.AppByID(r.Context(), p.AppID)
	if errors.Is(err, state.ErrNotFound) || err == nil && (app.Status != state.AppActive && app.Status != state.AppEvictedCold) {
		s.appProvisionError(w, state.ErrRegionalProvisionExpired)
		return
	}
	if err != nil {
		s.appProvisionError(w, err)
		return
	}
	personal, err := s.personalDirectoryOrg(r.Context(), acct.ID, app.OrgID)
	if err != nil {
		s.appProvisionError(w, err)
		return
	}
	if !personal || uuid.MustParse(app.AccountID).String() != p.AccountID || app.Slug != p.AppSlug || app.ComputeRegion != p.Region {
		s.appProvisionError(w, state.ErrConflict)
		return
	}
	var req api.CreateAppRequest
	if json.Unmarshal(p.Request, &req) != nil {
		s.appProvisionError(w, state.ErrConflict)
		return
	}
	writeJSON(w, status, api.CellAppProvisionResponse{ProvisionID: p.ProvisionID, GlobalAppID: p.GlobalAppID, Binding: api.CellAppBindingResponse{CellID: p.CellID, Region: p.Region, AccountID: p.AccountID, AppID: p.AppID, AppSlug: p.AppSlug}, Request: req, CreatedAt: p.CreatedAt})
}

func (s *server) appProvisionError(w http.ResponseWriter, err error) {
	var quota *state.QuotaError
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Provisioning receipt not found", "No matching receipt exists for this account and cell."))
	case errors.Is(err, state.ErrRegionalProvisionExpired):
		api.WriteProblem(w, api.NewProblem(http.StatusGone, api.CodeConflict, "Provisioned app removed", "This receipt cannot create a replacement app."))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Provisioning intent conflicts", "Retained provisioning intent and ownership cannot be replaced."))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("invalid provisioning intent"))
	case api.AsProblem(err) != nil:
		api.WriteProblem(w, api.AsProblem(err))
	case state.ServiceCapacityProblem(err) != nil:
		api.WriteProblem(w, state.ServiceCapacityProblem(err))
	case errors.As(err, &quota):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodePlanLimitApps, "Regional app quota exceeded", "The regional account has reached its app limit.").WithLimit(int64(quota.Limit), int64(quota.Observed)).WithDocs("https://gregale.dev/docs/quotas"))
	default:
		api.WriteProblem(w, api.ErrCapacity("could not access regional app provisioning"))
	}
}
