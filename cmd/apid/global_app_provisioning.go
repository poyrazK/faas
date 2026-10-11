package main

// adr: 797
import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) putGlobalAppProvisioning(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.globalDirectoryAvailable(w) {
		return
	}
	store, ok := s.globalDirectory.(globaldirectory.AppProvisionStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("global app provisioning unavailable"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.GlobalImageReleaseRequestTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	if _, err := s.globalDirectory.AppBySlug(ctx, globalOwnerID(r, acct), r.PathValue("slug")); err != nil {
		s.globalDirectoryError(w, r, acct, err)
		return
	}
	var req api.PutGlobalAppProvisioningRequest
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.GlobalAppProvisionRequestMaxBytes))
	d.DisallowUnknownFields()
	if d.Decode(&req) != nil || d.Decode(&struct{}{}) != io.EOF || len(req.Targets) == 0 || len(req.Targets) > api.GlobalImageReleaseTargetsMax {
		api.WriteProblem(w, api.ErrValidation("send one bounded set of regional provisioning targets"))
		return
	}
	specs, keys, err := s.verifyProvisioningSpecs(r, acct, req)
	if err != nil {
		s.globalDirectoryError(w, r, acct, err)
		return
	}
	plans, err := store.PlanAppProvisioning(ctx, globalOwnerID(r, acct), r.PathValue("slug"), specs)
	if err != nil {
		var bound *globaldirectory.ReleaseLimitError
		if errors.As(err, &bound) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Provisioning target bound reached", "The global app has reached the retained regional target bound.").WithLimit(bound.Limit, bound.Observed).WithDocs("https://gregale.dev/docs/global-app-directory#regional-app-provisioning"))
		} else {
			s.globalDirectoryError(w, r, acct, err)
		}
		return
	}
	out := s.reconcileAppProvisioning(ctx, r.PathValue("slug"), store, plans, keys, globalOwnerID(r, acct))
	writeJSON(w, http.StatusAccepted, out)
}

func (s *server) verifyProvisioningSpecs(r *http.Request, acct state.Account, req api.PutGlobalAppProvisioningRequest) ([]globaldirectory.AppProvisionSpec, map[string]string, error) {
	store := s.globalDirectory.(globaldirectory.AppProvisionStore)
	old, err := store.ListAppProvisioning(r.Context(), globalOwnerID(r, acct), r.PathValue("slug"))
	if err != nil {
		return nil, nil, err
	}
	byRegion := make(map[string]globaldirectory.AppProvision)
	for _, p := range old {
		byRegion[p.Region] = p
	}
	specs := make([]globaldirectory.AppProvisionSpec, 0, len(req.Targets))
	keys := make(map[string]string)
	for _, t := range req.Targets {
		if t.Region == "" || !api.ValidComputeRegion(t.Region) || !globaldirectory.ValidID(t.RegionalAccountID) || !api.ValidAPIKeyFormat(t.RegionalAPIKey) || keys[t.Region] != "" {
			return nil, nil, globaldirectory.ErrConflict
		}
		if t.App.Slug == "" {
			t.App.Slug = r.PathValue("slug")
		}
		if t.App.ComputeRegion == "" {
			t.App.ComputeRegion = t.Region
		}
		if t.App.ComputeRegion != t.Region || !globaldirectory.ValidSlug(t.App.Slug) || t.App.OverflowNode != nil {
			return nil, nil, globaldirectory.ErrConflict
		}
		cell, err := s.globalDirectory.CellByRegion(r.Context(), t.Region)
		if err != nil {
			return nil, nil, err
		}
		cancelled := false
		if p, ok := byRegion[t.Region]; ok {
			cancelled = p.Status == globaldirectory.ProvisionCancelled
			if !cancelled && (p.Cell.ID != cell.ID || p.Cell.Generation != cell.Generation || p.Cell.APIBase != cell.APIBase || !cell.Enabled) {
				return nil, nil, globaldirectory.ErrStaleCell
			}
		}
		if !cancelled {
			if err := s.globalVerifier.VerifyAccount(r.Context(), cell, t.RegionalAPIKey, t.RegionalAccountID); err != nil {
				return nil, nil, err
			}
		}
		raw, err := json.Marshal(t.App)
		if err != nil {
			return nil, nil, err
		}
		specs = append(specs, globaldirectory.AppProvisionSpec{VerifiedCell: cell, Region: t.Region, AccountID: t.RegionalAccountID, Request: raw})
		keys[t.Region] = t.RegionalAPIKey
	}
	return specs, keys, nil
}

func (s *server) reconcileAppProvisioning(ctx context.Context, slug string, store globaldirectory.AppProvisionStore, plans []globaldirectory.AppProvision, keys map[string]string, owner string) []api.GlobalAppProvisionTargetResponse {
	out := make([]api.GlobalAppProvisionTargetResponse, len(plans))
	work := make(chan int, len(plans))
	for i := range plans {
		work <- i
	}
	close(work)
	var wg sync.WaitGroup
	for range api.GlobalImageReleaseConcurrentTargets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				result := globaldirectory.ReconcileAppProvision(ctx, s.globalDirectory, store, s.globalVerifier, owner, slug, plans[i], keys[plans[i].Region])
				out[i] = api.GlobalAppProvisionTargetResponse{Region: result.Plan.Region, ProvisionID: result.Plan.ID, Status: result.Plan.Status, Diagnostic: result.Diagnostic}
				if result.Target != nil {
					t := globalTargetResponse(*result.Target)
					out[i].Target = &t
				}
			}
		}()
	}
	wg.Wait()
	return out
}

func (s *server) getGlobalAppProvisioning(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.globalDirectoryAvailable(w) {
		return
	}
	store, ok := s.globalDirectory.(globaldirectory.AppProvisionStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("global app provisioning unavailable"))
		return
	}
	plans, err := store.ListAppProvisioning(r.Context(), globalOwnerID(r, acct), r.PathValue("slug"))
	if err != nil {
		s.globalDirectoryError(w, r, acct, err)
		return
	}
	out := make([]api.GlobalAppProvisionTargetResponse, 0, len(plans))
	for _, p := range plans {
		out = append(out, api.GlobalAppProvisionTargetResponse{Region: p.Region, ProvisionID: p.ID, Status: p.Status})
	}
	writeJSON(w, http.StatusOK, out)
}

func appProvisionRequestMatches(p state.RegionalAppProvision, req state.RegionalAppProvision) bool {
	return p.GlobalAppID == req.GlobalAppID && p.CellID == req.CellID && p.Region == req.Region && p.AppSlug == req.AppSlug && string(p.Request) == string(req.Request)
}

func (s *server) replayAppProvision(w http.ResponseWriter, r *http.Request, acct state.Account, store state.RegionalAppProvisionStore, p state.RegionalAppProvision) bool {
	prior, err := store.RegionalAppProvisionReceipt(r.Context(), p.CellID, acct.ID, p.ProvisionID)
	if errors.Is(err, state.ErrNotFound) {
		return false
	}
	if err != nil {
		s.appProvisionError(w, err)
		return true
	}
	if !appProvisionRequestMatches(prior, p) {
		s.appProvisionError(w, state.ErrConflict)
		return true
	}
	s.writeCellAppProvision(w, r, acct, prior, http.StatusOK)
	return true
}
