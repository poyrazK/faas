package datapipelines

import (
	"context"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func (h *Handler) appendAlerts(ctx context.Context, data *dashboard.DataPipelinesData, p datapipeline.Pipeline) error {
	store, ok := h.Service.Store.(datapipeline.AlertStore)
	if !ok || p.State == "deleted" || p.DesiredState == "deleted" {
		return nil
	}
	a, err := store.Alerts(ctx, p.AccountID, p.ID)
	if err != nil {
		return err
	}
	v := dashboard.DataPipelineAlerts{PipelineID: p.ID, Name: p.Name, Revision: a.Revision, Enabled: a.Enabled, IncidentID: a.IncidentID, IncidentCode: a.IncidentCode}
	for _, d := range a.History {
		v.History = append(v.History, dashboard.DataPipelineAlertDelivery{ID: d.ID, IncidentID: d.IncidentID, Kind: d.Kind, Code: d.Code, Status: d.Status, Result: d.Result, OccurredAt: d.OccurredAt, Attempts: d.Attempts, ResponseCode: d.ResponseCode})
	}
	data.Alerts = append(data.Alerts, v)
	return nil
}

func (h *Handler) manageDashboardAlerts(r *http.Request, account string) (string, error) {
	revision, err := strconv.ParseInt(r.FormValue("alert_revision"), 10, 64)
	if err != nil || revision < 0 {
		return "", datapipeline.ErrInvalid
	}
	enabled := r.FormValue("action") == "alerts_enable"
	if enabled && r.FormValue("confirm_alerts") != "yes" {
		return "", datapipeline.ErrInvalid
	}
	return "", h.Service.ConfigureHealthAlerts(r.Context(), account, r.FormValue("pipeline_id"), revision, enabled, r.FormValue("alert_url"), r.FormValue("alert_secret"))
}
