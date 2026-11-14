package datapipelines

import (
	"net/http"
	"slices"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func (h *Handler) manageDashboardOperations(r *http.Request, account string) (string, error) {
	revision, err := strconv.ParseInt(r.FormValue("revision"), 10, 64)
	if err != nil {
		return "", datapipeline.ErrInvalid
	}
	id := r.FormValue("pipeline_id")
	req := api.DataPipelineOperationsRequest{ExpectedRevision: revision, Action: r.FormValue("operation")}
	if req.Action != "configure" {
		return "", h.Service.Operations(r.Context(), account, id, req)
	}
	p, err := h.Service.Store.Get(r.Context(), account, id)
	if err != nil {
		return "", err
	}
	c := api.DataPipelineOperations{Version: 1}
	if p.Operations != nil {
		c = *p.Operations
	}
	switch r.FormValue("operation_type") {
	case "workload":
		timeout, err := strconv.ParseInt(r.FormValue("query_timeout"), 10, 64)
		if err != nil {
			return "", datapipeline.ErrInvalid
		}
		batches, err := strconv.Atoi(r.FormValue("batches"))
		if err != nil {
			return "", datapipeline.ErrInvalid
		}
		w := &api.DataPipelineWorkload{QueryTimeoutMillis: timeout, BatchesPerTick: batches}
		if r.FormValue("window_start") != "" || r.FormValue("window_end") != "" {
			start, err := strconv.Atoi(r.FormValue("window_start"))
			if err != nil {
				return "", datapipeline.ErrInvalid
			}
			end, err := strconv.Atoi(r.FormValue("window_end"))
			if err != nil {
				return "", datapipeline.ErrInvalid
			}
			w.Window = &api.DataPipelineWorkWindow{StartMinuteUTC: start, EndMinuteUTC: end}
		}
		c.Workload = w
	case "snapshot":
		if c.Snapshot == nil {
			return "", datapipeline.ErrInvalid
		}
		copy := *c.Snapshot
		c.Snapshot = &copy
		c.Snapshot.EverySeconds, err = strconv.ParseInt(r.FormValue("every_seconds"), 10, 64)
		if err != nil {
			return "", datapipeline.ErrInvalid
		}
	case "quality_add", "quality_remove", "quality_disable":
		if r.FormValue("operation_type") == "quality_disable" {
			c.Quality = nil
			break
		}
		q := api.DataPipelineQualitySchedule{}
		if c.Quality != nil {
			q = *c.Quality
			q.Rules = slices.Clone(q.Rules)
		}
		name := r.FormValue("rule_name")
		q.Rules = slices.DeleteFunc(q.Rules, func(v api.DataPipelineQualityRule) bool { return v.Name == name })
		if r.FormValue("operation_type") == "quality_add" {
			q.EverySeconds, err = strconv.ParseInt(r.FormValue("every_seconds"), 10, 64)
			if err != nil {
				return "", datapipeline.ErrInvalid
			}
			q.Rules = append(q.Rules, api.DataPipelineQualityRule{Name: name, Kind: r.FormValue("rule_kind"), Table: r.FormValue("table"), Columns: evolutionLines(r.FormValue("columns")), Minimum: r.FormValue("minimum"), Maximum: r.FormValue("maximum"), ReferenceTable: r.FormValue("reference_table"), ReferenceColumns: evolutionLines(r.FormValue("reference_columns"))})
		}
		if len(q.Rules) == 0 {
			c.Quality = nil
		} else {
			c.Quality = &q
		}
	default:
		return "", datapipeline.ErrInvalid
	}
	req.Config = &c
	return "", h.Service.Operations(r.Context(), account, id, req)
}

func dashboardSnapshotConfig(r *http.Request) (*api.DataPipelineOperations, error) {
	if r.FormValue("source_mode") != "snapshot" {
		return nil, nil
	}
	seconds, err := strconv.ParseInt(r.FormValue("snapshot_every_seconds"), 10, 64)
	if err != nil {
		return nil, datapipeline.ErrInvalid
	}
	return &api.DataPipelineOperations{Version: 1, Snapshot: &api.DataPipelineSnapshotSchedule{EverySeconds: seconds}}, nil
}
