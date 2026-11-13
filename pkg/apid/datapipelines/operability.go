package datapipelines

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func publicSetup(s datapipeline.Setup) api.DataPipelineSetup {
	v := setupView(s)
	return api.DataPipelineSetup{ID: s.ID, Request: s.Request.PublicRequest(), DatabaseID: s.DatabaseID, PipelineID: s.PipelineID, State: s.State, Phase: s.Phase, Reason: s.Reason, Revision: s.Revision, DataAPIAppID: s.DataAPIPlan.AppID, DeploymentID: s.DataAPIPlan.DeploymentID, Endpoint: s.DataAPIPlan.Endpoint, Region: s.MirrorPlan.Region, StorageLimitBytes: s.MirrorPlan.StorageLimitBytes, Progress: v.Progress, Recovery: v.Remediation, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt}
}
func (h *Handler) PlanSetup(w http.ResponseWriter, r *http.Request, account string) {
	if !h.ready(w) {
		return
	}
	var req api.DataPipelineSetupRequest
	if httpkit.DecodeJSONSized(r, &req, api.DataPipelineRequestMaxBytes) != nil {
		problem(w, datapipeline.ErrInvalid)
		return
	}
	plan, err := h.Service.PlanSetup(r.Context(), account, datapipeline.SetupInput(req))
	if err != nil {
		problem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, 200, plan)
}
func (h *Handler) StartSetup(w http.ResponseWriter, r *http.Request, account string) {
	if !h.ready(w) {
		return
	}
	var req api.DataPipelineSetupRequest
	if httpkit.DecodeJSONSized(r, &req, api.DataPipelineRequestMaxBytes) != nil {
		problem(w, datapipeline.ErrInvalid)
		return
	}
	job, err := h.Service.StartSetup(r.Context(), account, datapipeline.SetupInput(req))
	if err != nil {
		problem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, 202, publicSetup(job))
}
func (h *Handler) Setups(w http.ResponseWriter, r *http.Request, account string) {
	if !h.ready(w) {
		return
	}
	jobs, err := h.Service.Setups(r.Context(), account)
	if err != nil {
		problem(w, err)
		return
	}
	out := api.DataPipelineSetupList{Items: make([]api.DataPipelineSetup, 0, len(jobs))}
	for _, job := range jobs {
		out.Items = append(out.Items, publicSetup(job))
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, 200, out)
}
func (h *Handler) GetSetup(w http.ResponseWriter, r *http.Request, account string) {
	if !h.ready(w) {
		return
	}
	job, err := h.Service.GetSetup(r.Context(), account, r.PathValue("id"))
	if err != nil {
		problem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, 200, publicSetup(job))
}
func (h *Handler) ControlSetup(w http.ResponseWriter, r *http.Request, account string) {
	if !h.ready(w) {
		return
	}
	var req api.DataPipelineSetupControlRequest
	if httpkit.DecodeJSONSized(r, &req, api.DataPipelineRequestMaxBytes) != nil {
		problem(w, datapipeline.ErrInvalid)
		return
	}
	job, err := h.Service.ControlSetup(r.Context(), account, r.PathValue("id"), req.ExpectedRevision, req.Action)
	if err != nil {
		problem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, 202, publicSetup(job))
}
func (h *Handler) Freshness(w http.ResponseWriter, r *http.Request, account string) {
	if !h.ready(w) {
		return
	}
	var req api.DataPipelineFreshnessRequest
	if httpkit.DecodeJSONSized(r, &req, api.DataPipelineRequestMaxBytes) != nil {
		problem(w, datapipeline.ErrInvalid)
		return
	}
	if err := h.Service.ConfigureFreshness(r.Context(), account, r.PathValue("id"), req); err != nil {
		problem(w, err)
		return
	}
	h.writeAcceptedPipeline(w, r, account)
}
func (h *Handler) Indexes(w http.ResponseWriter, r *http.Request, account string) {
	if !h.ready(w) {
		return
	}
	var req api.DataPipelineIndexRequest
	if httpkit.DecodeJSONSized(r, &req, api.DataPipelineRequestMaxBytes) != nil {
		problem(w, datapipeline.ErrInvalid)
		return
	}
	if err := h.Service.Indexes(r.Context(), account, r.PathValue("id"), req); err != nil {
		problem(w, err)
		return
	}
	h.writeAcceptedPipeline(w, r, account)
}
func (h *Handler) writeAcceptedPipeline(w http.ResponseWriter, r *http.Request, account string) {
	p, err := h.Service.Get(r.Context(), account, r.PathValue("id"))
	if err != nil {
		problem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, 202, p)
}
