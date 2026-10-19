package datapipelines

import (
	"net/http"
	"slices"
	"strconv"

	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func tableSelectionView(p datapipeline.Pipeline, data dashboard.DataPipelinesData) dashboard.DataPipelineTableSelection {
	v := dashboard.DataPipelineTableSelection{PipelineID: p.ID, ConnectionID: p.SourceConnectionID, Name: p.Name, Revision: p.Revision, Tables: p.Tables}
	v.Removable = datapipeline.RemovableTables(p)
	active := p.SchemaChange != nil && p.SchemaChange.State != "complete" && p.SchemaChange.State != "withdrawn"
	v.CanDiscard = active && len(p.SchemaChange.AddedTables) > 0 && p.SchemaApproval == ""
	v.CanRequest = data.Check != nil && data.Check.Fresh && !active && p.State == "live" && p.DesiredState == "running" && p.ObservationFresh && !p.VerifyRequested
	if p.VerificationProgress != nil && p.VerificationProgress.State == "running" {
		v.CanRequest = false
	}
	if p.ReaderChange != nil && p.ReaderChange.State == "approved" {
		v.CanRequest = false
	}
	if data.Check != nil {
		for _, table := range data.Check.Tables {
			if table.Supported && !slices.Contains(p.Tables, table.Name) {
				v.Choices = append(v.Choices, table.Name)
			}
		}
	}
	v.CanRequest = v.CanRequest && len(v.Choices) > 0
	return v
}

func tableSelectionViews(pipelines []datapipeline.Pipeline, data dashboard.DataPipelinesData) []dashboard.DataPipelineTableSelection {
	views := []dashboard.DataPipelineTableSelection{}
	for _, p := range pipelines {
		if p.State == "deleted" {
			continue
		}
		scope := data
		if p.SourceConnectionID != data.ConnectionID {
			scope.Check = nil
		}
		views = append(views, tableSelectionView(p, scope))
	}
	return views
}
func (h *Handler) manageDashboardTables(r *http.Request, account string) (string, error) {
	revision, err := strconv.ParseInt(r.FormValue("revision"), 10, 64)
	if err != nil {
		return "", datapipeline.ErrInvalid
	}
	if r.FormValue("action") == "tables_cancel" {
		err = h.Service.CancelTables(r.Context(), account, r.FormValue("pipeline_id"), revision)
	} else {
		err = h.Service.RequestTables(r.Context(), account, r.FormValue("pipeline_id"), revision, r.Form["tables"])
	}
	return r.FormValue("connection_id"), err
}
