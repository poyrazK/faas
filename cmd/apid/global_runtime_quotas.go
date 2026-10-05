package main

// adr: 612
import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) runtimeQuotaStore(w http.ResponseWriter, r *http.Request) (globaldirectory.RuntimeQuotaStore, bool) {
	if !globaldirectory.ValidID(r.PathValue("allocation_id")) || !globaldirectory.ValidSlug(r.PathValue("slug")) {
		api.WriteProblem(w, api.ErrValidation("use a canonical global slug and nonzero allocation UUID"))
		return nil, false
	}
	store, ok := s.globalDirectory.(globaldirectory.RuntimeQuotaStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("global runtime quota accounting is unavailable"))
	}
	return store, ok
}

func (s *server) putGlobalRuntimeQuota(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.runtimeQuotaStore(w, r)
	if !ok {
		return
	}
	var req api.PutGlobalRuntimeQuotaRequest
	if !decodeGlobalDirectoryRequest(w, r, &req) {
		return
	}
	if !validGlobalRuntimeQuotaInput(req) {
		api.WriteProblem(w, api.ErrValidation("send positive, unique shares for every selected region and an optional selected rollout region"))
		return
	}
	p := globalOwner(r, acct)
	input := globaldirectory.RuntimeQuotaRequest{OwnerID: p.Owner.ID, ActorID: acct.ID, Slug: r.PathValue("slug"), AllocationID: r.PathValue("allocation_id"), ExpectedPlan: p.Plan, RolloutRegion: req.RolloutRegion}
	for _, share := range req.Shares {
		input.Shares = append(input.Shares, globaldirectory.RuntimeQuotaShare{Region: share.Region, Concurrency: share.Concurrency})
	}
	a, err := store.ReserveRuntimeQuota(r.Context(), input)
	if err != nil {
		s.globalRuntimeQuotaError(w, r, acct, err)
		return
	}
	s.audit.Emit(r.Context(), "global_runtime_quota.reserved", &acct.ID, map[string]any{"global_app_id": a.GlobalAppID, "allocation_id": a.AllocationID, "generation": a.Generation})
	writeJSON(w, http.StatusOK, globalRuntimeQuotaResponse(a))
}

func (s *server) getGlobalRuntimeQuota(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.runtimeQuotaStore(w, r)
	if !ok {
		return
	}
	a, err := store.RuntimeQuota(r.Context(), globalOwnerID(r, acct), r.PathValue("slug"), r.PathValue("allocation_id"))
	if err != nil {
		s.globalRuntimeQuotaError(w, r, acct, err)
		return
	}
	writeJSON(w, http.StatusOK, globalRuntimeQuotaResponse(a))
}

func (s *server) retireGlobalRuntimeQuota(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.runtimeQuotaStore(w, r)
	if !ok {
		return
	}
	a, err := store.RetireRuntimeQuota(r.Context(), globalOwnerID(r, acct), acct.ID, r.PathValue("slug"), r.PathValue("allocation_id"))
	if err != nil {
		s.globalRuntimeQuotaError(w, r, acct, err)
		return
	}
	s.audit.Emit(r.Context(), "global_runtime_quota.retired", &acct.ID, map[string]any{"global_app_id": a.GlobalAppID, "allocation_id": a.AllocationID, "generation": a.Generation, "retirement_id": a.RetirementID})
	writeJSON(w, http.StatusOK, globalRuntimeQuotaResponse(a))
}

func validGlobalRuntimeQuotaInput(req api.PutGlobalRuntimeQuotaRequest) bool {
	if len(req.Shares) == 0 || len(req.Shares) > api.GlobalRuntimeQuotaTargetsMax {
		return false
	}
	seen := make(map[string]bool, len(req.Shares))
	for _, share := range req.Shares {
		if share.Region == "" || !api.ValidComputeRegion(share.Region) || share.Concurrency < 1 || seen[share.Region] {
			return false
		}
		seen[share.Region] = true
	}
	return req.RolloutRegion == "" || seen[req.RolloutRegion]
}

func (s *server) globalRuntimeQuotaError(w http.ResponseWriter, r *http.Request, acct state.Account, err error) {
	var bound *globaldirectory.RuntimeQuotaLimitError
	if errors.As(err, &bound) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeGlobalRuntimeQuotaLimit, "Global runtime quota exhausted", "Unreleased reservations plus the requested shares exceed the tenant plan's "+bound.Kind+" capacity.").WithLimit(bound.Limit, bound.Observed).WithDocs("https://gregale.dev/docs/global-app-directory#runtime-quota-reservations"))
		return
	}
	s.globalDirectoryError(w, r, acct, err)
}

func globalRuntimeQuotaResponse(a globaldirectory.RuntimeQuotaAllocation) api.GlobalRuntimeQuotaResponse {
	status := "reserved"
	var retirementID *string
	if a.RetiredAt != nil {
		retirementID = &a.RetirementID
		status = "retired"
		for _, target := range a.Targets {
			if target.ReleasedAt == nil {
				status = "retiring"
			}
		}
	}
	p := a.Policy
	out := api.GlobalRuntimeQuotaResponse{GlobalAppID: a.GlobalAppID, AllocationID: a.AllocationID, Generation: a.Generation, Policy: api.GlobalRuntimeQuotaPolicy(p), Status: status, CreatedAt: a.CreatedAt, RetirementID: retirementID, RetiredAt: a.RetiredAt, Targets: make([]api.GlobalRuntimeQuotaTargetResponse, 0, len(a.Targets))}
	for _, t := range a.Targets {
		out.Targets = append(out.Targets, api.GlobalRuntimeQuotaTargetResponse{CellID: t.CellID, Region: t.Region, CellGeneration: t.CellGeneration, RegionalAccountID: t.RegionalAccountID, RegionalOrgID: t.RegionalOrgID, RegionalAppID: t.RegionalAppID, RegionalAppSlug: t.RegionalAppSlug, Concurrency: t.Concurrency, RolloutConcurrency: t.RolloutConcurrency, IssuedAt: t.IssuedAt, ReleasedAt: t.ReleasedAt})
	}
	return out
}
