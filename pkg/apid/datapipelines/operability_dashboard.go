package datapipelines

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func dashboardSetupInput(r *http.Request) (datapipeline.SetupRequest, error) {
	serving, err := dashboardServing(r)
	if err != nil {
		return datapipeline.SetupRequest{}, err
	}
	var subjects []string
	if text := r.FormValue("read_subjects"); text != "" {
		subjects = strings.Split(text, ",")
		for i := range subjects {
			subjects[i] = strings.TrimSpace(subjects[i])
		}
	}
	seconds := int64(0)
	if text := r.FormValue("freshness_target_seconds"); text != "" {
		seconds, err = strconv.ParseInt(text, 10, 64)
		if err != nil {
			return datapipeline.SetupRequest{}, datapipeline.ErrInvalid
		}
	}
	return datapipeline.SetupRequest{Name: r.FormValue("name"), SourceConnectionID: r.FormValue("connection_id"), DatabaseID: r.FormValue("database_id"), Region: r.FormValue("region"), Tables: r.Form["tables"], ReadSubjects: subjects, Serving: serving, FreshnessTargetSeconds: seconds, DataAPIName: r.FormValue("data_api_name"), AuthAppID: r.FormValue("auth_app_id")}, nil
}
func (h *Handler) PlanDashboardSetup(r *http.Request, account string) (api.DataPipelineSetupPlan, error) {
	req, err := dashboardSetupInput(r)
	if err != nil {
		return api.DataPipelineSetupPlan{}, err
	}
	return h.Service.PlanSetup(r.Context(), account, req)
}
func (h *Handler) manageDashboardOperability(r *http.Request, account string) (string, error) {
	revision, err := strconv.ParseInt(r.FormValue("revision"), 10, 64)
	if err != nil {
		return "", datapipeline.ErrInvalid
	}
	id := r.FormValue("pipeline_id")
	if r.FormValue("action") == "freshness" {
		seconds, err := strconv.ParseInt(r.FormValue("target_seconds"), 10, 64)
		if err != nil {
			return "", datapipeline.ErrInvalid
		}
		return "", h.Service.ConfigureFreshness(r.Context(), account, id, api.DataPipelineFreshnessRequest{ExpectedRevision: revision, TargetSeconds: seconds})
	}
	req := api.DataPipelineIndexRequest{ExpectedRevision: revision, Action: r.FormValue("index_action"), AdviceID: r.FormValue("advice_id"), Table: r.FormValue("table")}
	if text := r.FormValue("max_index_bytes"); text != "" {
		req.MaxIndexBytes, err = strconv.ParseInt(text, 10, 64)
		if err != nil {
			return "", datapipeline.ErrInvalid
		}
	}
	if text := r.FormValue("filters"); text != "" {
		req.Filters = strings.Split(text, ",")
		for i := range req.Filters {
			req.Filters[i] = strings.TrimSpace(req.Filters[i])
		}
	}
	if text := r.FormValue("order_by"); text != "" {
		for _, name := range strings.Split(text, ",") {
			name = strings.TrimSpace(name)
			req.OrderBy = append(req.OrderBy, api.DataPipelineIndexOrder{Column: strings.TrimPrefix(name, "-"), Descending: strings.HasPrefix(name, "-")})
		}
	}
	return "", h.Service.Indexes(r.Context(), account, id, req)
}
