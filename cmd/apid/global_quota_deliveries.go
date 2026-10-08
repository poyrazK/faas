package main

// adr: 775
import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) quotaDeliveryStore(w http.ResponseWriter, r *http.Request) (globaldirectory.QuotaDeliveryStore, bool) {
	if !globaldirectory.ValidSlug(r.PathValue("slug")) || !globaldirectory.ValidID(r.PathValue("allocation_id")) || !globaldirectory.ValidID(r.PathValue("delivery_id")) {
		api.WriteProblem(w, api.ErrValidation("use the original global slug and canonical allocation and delivery UUIDs"))
		return nil, false
	}
	j, ok := s.globalDirectory.(globaldirectory.QuotaDeliveryStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("durable quota delivery is unavailable"))
	}
	return j, ok
}

func (s *server) queueGlobalQuotaDelivery(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := s.quotaDeliveryStore(w, r); !ok {
		return
	}
	if s.globalQuotaAuthority == nil {
		api.WriteProblem(w, api.ErrCapacity("directory quota authority is unavailable"))
		return
	}
	var req api.QueueGlobalQuotaDeliveryRequest
	if !decodeGlobalDirectoryRequest(w, r, &req) {
		return
	}
	if req.Region == "" || !api.ValidComputeRegion(req.Region) || (req.Purpose != globaldirectory.QuotaDeliveryGrant && req.Purpose != globaldirectory.QuotaDeliveryRetirement) || (req.Purpose == globaldirectory.QuotaDeliveryGrant && !globaldirectory.ValidID(req.ConsentID)) || (req.Purpose == globaldirectory.QuotaDeliveryRetirement && req.ConsentID != "") {
		api.WriteProblem(w, api.ErrValidation("send a selected region and grant with original runtime consent, or retirement without consent"))
		return
	}
	j, err := s.globalQuotaAuthority.QueueDelivery(r.Context(), globaldirectory.QuotaDeliveryRequest{OwnerID: globalOwnerID(r, acct), Slug: r.PathValue("slug"), AllocationID: r.PathValue("allocation_id"), ID: r.PathValue("delivery_id"), Region: req.Region, Purpose: req.Purpose, ConsentID: req.ConsentID})
	s.writeGlobalQuotaDelivery(w, r, acct, j, err)
}

func (s *server) getGlobalQuotaDelivery(w http.ResponseWriter, r *http.Request, acct state.Account) {
	j, ok := s.quotaDeliveryStore(w, r)
	if !ok {
		return
	}
	out, err := j.QuotaDelivery(r.Context(), globalOwnerID(r, acct), r.PathValue("slug"), r.PathValue("delivery_id"))
	if err == nil && out.AllocationID != r.PathValue("allocation_id") {
		err = globaldirectory.ErrNotFound
	}
	s.writeGlobalQuotaDelivery(w, r, acct, out, err)
}

func (s *server) retryGlobalQuotaDelivery(w http.ResponseWriter, r *http.Request, acct state.Account) {
	j, ok := s.quotaDeliveryStore(w, r)
	if !ok {
		return
	}
	owner, slug, id := globalOwnerID(r, acct), r.PathValue("slug"), r.PathValue("delivery_id")
	out, err := j.QuotaDelivery(r.Context(), owner, slug, id)
	if err == nil && out.AllocationID != r.PathValue("allocation_id") {
		err = globaldirectory.ErrNotFound
	}
	if err == nil {
		out, err = j.RetryQuotaDelivery(r.Context(), owner, slug, id)
	}
	s.writeGlobalQuotaDelivery(w, r, acct, out, err)
}

func (s *server) writeGlobalQuotaDelivery(w http.ResponseWriter, r *http.Request, acct state.Account, j globaldirectory.QuotaDeliveryJob, err error) {
	var bound *globaldirectory.QuotaDeliveryLimitError
	switch {
	case err == nil:
		if r.Method != http.MethodGet {
			s.audit.Emit(r.Context(), "global_runtime_quota.delivery_queued", &acct.ID, map[string]any{"delivery_id": j.ID, "allocation_id": j.AllocationID, "purpose": j.Purpose, "status": j.Status})
		}
		writeJSON(w, http.StatusOK, globalQuotaDeliveryResponse(j))
	case errors.As(err, &bound):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Quota delivery limit reached", "Wait for outstanding deliveries or inspect blocked work.").WithLimit(bound.Limit, bound.Observed).WithDocs("https://gregale.dev/docs/global-app-directory#durable-quota-delivery"))
	case errors.Is(err, globaldirectory.ErrQuotaDeliveryClaim):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Quota delivery is leased", "The recovery worker must finish or reclaim this delivery before an explicit retry."))
	case errors.Is(err, globaldirectory.ErrQuotaDeliveryLegacy):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Original quota delivery unavailable", "This allocation was issued without a durable delivery. Retire it before reserving a new allocation for this region."))
	default:
		s.runtimeQuotaTransportError(w, r, acct, err)
	}
}

func globalQuotaDeliveryResponse(j globaldirectory.QuotaDeliveryJob) api.GlobalQuotaDeliveryResponse {
	b := j.Delivery.Binding
	return api.GlobalQuotaDeliveryResponse{ID: j.ID, GlobalAppID: j.GlobalAppID, AllocationID: j.AllocationID, Generation: j.Grant.Generation, IssuerID: j.IssuerID, Purpose: j.Purpose, ConsentID: j.ConsentID, RetirementID: j.RetirementID,
		Binding:   api.GlobalRuntimeConsentBindingResponse{CellID: b.CellID, Region: b.Region, CellGeneration: b.CellGeneration, RegionalAccountID: b.RegionalAccountID, RegionalAppID: b.RegionalAppID, RegionalAppSlug: b.RegionalAppSlug, APIBase: b.APIBase},
		CreatedAt: j.CreatedAt, Status: j.Status, Attempts: j.Attempts, NextAttemptAt: j.NextAttemptAt, ClaimUntil: j.ClaimUntil, AcceptedAt: j.AcceptedAt, LastCode: j.LastCode}
}

func (s *server) runGlobalQuotaDeliveryRecovery(ctx context.Context) {
	if !s.globalQuotaDeliveryWorker || s.globalQuotaAuthority == nil {
		return
	}
	j, ok := s.globalDirectory.(globaldirectory.QuotaDeliveryStore)
	if !ok {
		return
	}
	w := globaldirectory.QuotaDeliveryWorker{Authority: *s.globalQuotaAuthority, Store: j, Verifier: s.globalVerifier, OnError: func() { s.log.Warn("global quota delivery incomplete; original work retained") }}
	_ = w.Run(ctx)
}
