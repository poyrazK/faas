// Package datapipelines is the authenticated customer data-pipeline API domain.
// It records intent; no handler connects to a customer's database.
package datapipelines

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/datapipeline"
)

type Handler struct{ Service *datapipeline.Service }

func problem(w http.ResponseWriter, err error) {
	status, code, detail := 503, "data_pipeline_unavailable", "Data pipelines are unavailable."
	switch {
	case errors.Is(err, datapipeline.ErrInvalid):
		status, code, detail = 400, "data_pipeline_invalid", "Check the request fields and supported pipeline configuration."
	case errors.Is(err, datapipeline.ErrNotFound):
		status, code, detail = 404, "data_pipeline_not_found", "The data resource was not found."
	case errors.Is(err, datapipeline.ErrConflict):
		status, code, detail = 409, "data_pipeline_conflict", "The resource changed, is already configured differently, or is still in use. Refresh its state before retrying."
	case errors.Is(err, datapipeline.ErrQuota):
		status, code, detail = 403, "data_pipeline_limit", "The account has reached its data pipeline limit."
	}
	p := api.NewProblem(status, code, http.StatusText(status), detail).WithDocs("https://gregale.dev/docs/data-pipelines")
	var quota *datapipeline.QuotaError
	if errors.As(err, &quota) {
		p.WithLimit(quota.Limit, quota.Observed)
	}
	api.WriteProblem(w, p)
}
func (h *Handler) ready(w http.ResponseWriter) bool {
	if h == nil || h.Service == nil || h.Service.Store == nil {
		problem(w, datapipeline.ErrUnavailable)
		return false
	}
	return true
}

func (h *Handler) CreateConnection(w http.ResponseWriter, r *http.Request, account string) {
	if !h.ready(w) {
		return
	}
	var req api.CreateDataConnectionRequest
	if httpkit.DecodeJSONSized(r, &req, api.DataPipelineRequestMaxBytes) != nil {
		problem(w, datapipeline.ErrInvalid)
		return
	}
	c, err := h.Service.CreateConnection(r.Context(), account, req)
	if err != nil {
		problem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, 201, c)
}
func (h *Handler) Connections(w http.ResponseWriter, r *http.Request, account string) {
	if !h.ready(w) {
		return
	}
	items, err := h.Service.Store.Connections(r.Context(), account)
	if err != nil {
		problem(w, err)
		return
	}
	result := api.DataConnectionList{Items: make([]api.DataConnection, 0, len(items))}
	for _, item := range items {
		result.Items = append(result.Items, item.DataConnection)
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, 200, result)
}
func (h *Handler) RotateConnection(w http.ResponseWriter, r *http.Request, account string) {
	if !h.ready(w) {
		return
	}
	var req api.RotateDataConnectionRequest
	if httpkit.DecodeJSONSized(r, &req, api.DataPipelineRequestMaxBytes) != nil {
		problem(w, datapipeline.ErrInvalid)
		return
	}
	c, err := h.Service.RotateConnection(r.Context(), account, r.PathValue("id"), req)
	if err != nil {
		problem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, 200, c)
}
func (h *Handler) DeleteConnection(w http.ResponseWriter, r *http.Request, account string) {
	if !h.ready(w) {
		return
	}
	if err := h.Service.Store.DeleteConnection(r.Context(), account, r.PathValue("id")); err != nil {
		problem(w, err)
		return
	}
	w.WriteHeader(204)
}
func (h *Handler) Create(w http.ResponseWriter, r *http.Request, account string) {
	if !h.ready(w) {
		return
	}
	var req api.CreateDataPipelineRequest
	if httpkit.DecodeJSONSized(r, &req, api.DataPipelineRequestMaxBytes) != nil {
		problem(w, datapipeline.ErrInvalid)
		return
	}
	p, err := h.Service.Create(r.Context(), account, req)
	if err != nil {
		problem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, 202, p)
}
func (h *Handler) List(w http.ResponseWriter, r *http.Request, account string) {
	if !h.ready(w) {
		return
	}
	items, err := h.Service.Store.List(r.Context(), account)
	if err != nil {
		problem(w, err)
		return
	}
	result := api.DataPipelineList{Items: make([]api.DataPipeline, 0, len(items))}
	for _, item := range items {
		result.Items = append(result.Items, item.DataPipeline)
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, 200, result)
}
func (h *Handler) Get(w http.ResponseWriter, r *http.Request, account string) {
	if !h.ready(w) {
		return
	}
	item, err := h.Service.Store.Get(r.Context(), account, r.PathValue("id"))
	if err != nil {
		problem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, 200, item.DataPipeline)
}
func (h *Handler) Control(w http.ResponseWriter, r *http.Request, account string) {
	if !h.ready(w) {
		return
	}
	var req api.ControlDataPipelineRequest
	if httpkit.DecodeJSONSized(r, &req, api.DataPipelineRequestMaxBytes) != nil {
		problem(w, datapipeline.ErrInvalid)
		return
	}
	if req.Action == "resume" || req.Action == "resync" || req.Action == "verify" {
		if h.Service.Allowed == nil || !h.Service.Allowed(r.Context(), account) || ((req.Action == "resume" || req.Action == "resync") && h.Service.CanCreate != nil && !h.Service.CanCreate(r.Context(), account)) {
			problem(w, datapipeline.ErrUnavailable)
			return
		}
	}
	p, err := h.Service.Store.Control(r.Context(), account, r.PathValue("id"), req.ExpectedRevision, req.Action)
	if err != nil {
		problem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, 202, p.DataPipeline)
}
