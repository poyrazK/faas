package datapipelines

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func (h *Handler) Workspace(ctx context.Context, account, connection string) (dashboard.DataPipelinesData, error) {
	data := dashboard.DataPipelinesData{ConnectionID: connection}
	if h == nil || h.Service == nil || h.Service.Store == nil {
		return data, nil
	}
	data.Enabled = h.Service.Available(ctx, account)
	data.DataAPIEnabled = h.Service.PlanDataAPI != nil && h.Service.PrepareDataAPI != nil && h.Service.DeployDataAPI != nil && h.Service.DataAPIState != nil
	data.SetupEnabled = h.Service.PlanMirror != nil && h.Service.EnsureMirror != nil && h.Service.MirrorState != nil
	if setupStore, ok := h.Service.Store.(datapipeline.SetupStore); ok {
		setups, err := setupStore.Setups(ctx, account)
		if err != nil {
			return data, err
		}
		for _, setup := range setups {
			data.Setups = append(data.Setups, setupView(setup))
			if setup.State == "working" {
				data.RefreshSeconds = int(api.DataPipelineDashboardRefresh / time.Second)
			}
		}
	}
	connections, err := h.Service.Store.Connections(ctx, account)
	if err != nil {
		return data, err
	}
	for _, c := range connections {
		data.Connections = append(data.Connections, c.DataConnection)
	}
	pipelines, err := h.Service.Store.List(ctx, account)
	if err != nil {
		return data, err
	}
	for _, p := range pipelines {
		if err = h.appendAlerts(ctx, &data, p); err != nil {
			return data, err
		}
		if p.State != "deleted" {
			data.Readers = append(data.Readers, readersView(p))
		}
		if p.VerificationProgress != nil {
			data.Verifications = append(data.Verifications, verificationView(p))
		}
		if p.SchemaChange != nil && (p.SchemaChange.ID != "" || p.SchemaChange.Cancellation != nil) {
			data.SchemaChanges = append(data.SchemaChanges, schemaView(p))
		}
		if h.Service.CapacityStatus != nil && p.State != "deleted" {
			p.Capacity, err = h.Service.CapacityStatus(ctx, account, p.ID)
			if err != nil {
				return data, err
			}
		}
		data.Pipelines = append(data.Pipelines, p.DataPipeline)
		if p.State != "deleted" {
			if data.RefreshSeconds == 0 {
				data.RefreshSeconds = int(api.DataPipelinePollInterval / time.Second)
			}
		}
	}
	data.TableSelections = tableSelectionViews(pipelines, data)
	if connection == "" {
		return data, nil
	}
	c, err := h.Service.Store.Connection(ctx, account, connection)
	if err != nil {
		return data, err
	}
	data.ConnectionRevision = c.Revision
	check, err := h.Service.Store.Check(ctx, account, connection)
	if errors.Is(err, datapipeline.ErrNotFound) {
		return data, nil
	}
	if err != nil {
		return data, err
	}
	data.Check = &check.DataConnectionCheck
	data.TableSelections = tableSelectionViews(pipelines, data)
	if check.State == "pending" || check.State == "running" || (check.Capacity != nil && (check.Capacity.State == "ready" || (check.Capacity.Source != nil && check.Capacity.Source.State == "ready"))) {
		data.RefreshSeconds = int(api.DataPipelineDashboardRefresh / time.Second)
	}
	return data, nil
}

// DashboardAction adapts a bounded, authenticated and CSRF-verified form to the
// same service methods as the public API. The apid boundary verifies the cookie.
func (h *Handler) DashboardAction(w http.ResponseWriter, r *http.Request, account string) {
	if !h.ready(w) {
		return
	}
	if err := r.ParseForm(); err != nil {
		problem(w, datapipeline.ErrInvalid)
		return
	}
	connection, err := h.applyDashboardAction(r, account)
	if err != nil {
		problem(w, err)
		return
	}
	location := "/dashboard/data"
	if connection != "" {
		location += "?connection=" + url.QueryEscape(connection)
	}
	http.Redirect(w, r, location, http.StatusSeeOther)
}

func (h *Handler) applyDashboardAction(r *http.Request, account string) (string, error) {
	ctx := r.Context()
	id := r.FormValue("connection_id")
	switch r.FormValue("action") {
	case "alerts_enable", "alerts_disable":
		return h.manageDashboardAlerts(r, account)
	case "connection":
		c, err := h.Service.CreateConnection(ctx, account, api.CreateDataConnectionRequest{Name: r.FormValue("name"), ConnectionURL: r.FormValue("connection_url")})
		return c.ID, err
	case "rotate", "delete_connection":
		return h.manageDashboardConnection(r, account)
	case "setup", "setup_control":
		return h.manageDashboardSetup(r, account)
	case "verification_cancel":
		return h.manageDashboardVerification(r, account)
	case "tables_request", "tables_cancel":
		return h.manageDashboardTables(r, account)
	case "table_recovery_preview", "table_recovery_approve":
		return h.manageDashboardTableRecovery(r, account)
	case "table_cancellation_preview", "table_cancellation_approve", "table_cancellation_discard":
		return h.manageDashboardTableCancellation(r, account)
	case "table_removal_preview", "table_removal_approve", "table_removal_discard":
		return h.manageDashboardTableRemoval(r, account)
	case "readers_preview", "readers_approve", "readers_discard":
		return h.manageDashboardReaders(r, account)
	case "schema_approve", "schema_retry":
		return h.manageDashboardSchema(r, account)
	case "check":
		revision, err := strconv.ParseInt(r.FormValue("revision"), 10, 64)
		if err != nil {
			return "", datapipeline.ErrInvalid
		}
		_, err = h.Service.RequestCheck(ctx, account, id, api.RequestDataConnectionCheck{ExpectedRevision: revision, DatabaseID: r.FormValue("database_id")})
		return id, err
	case "create":
		subjects := strings.Split(r.FormValue("read_subjects"), ",")
		for i := range subjects {
			subjects[i] = strings.TrimSpace(subjects[i])
		}
		_, err := h.Service.Create(ctx, account, api.CreateDataPipelineRequest{Name: r.FormValue("name"), SourceConnectionID: id, DatabaseID: r.FormValue("database_id"), Tables: r.Form["tables"], ReadSubjects: subjects})
		return id, err
	case "control":
		if r.FormValue("control") == "delete" && r.FormValue("confirm_delete") != "yes" {
			return "", datapipeline.ErrInvalid
		}
		if r.FormValue("control") == "resync" && r.FormValue("confirm_resync") != "yes" {
			return "", datapipeline.ErrInvalid
		}
		revision, err := strconv.ParseInt(r.FormValue("revision"), 10, 64)
		if err != nil {
			return "", datapipeline.ErrInvalid
		}
		_, err = h.Service.Control(ctx, account, r.FormValue("pipeline_id"), api.ControlDataPipelineRequest{ExpectedRevision: revision, Action: r.FormValue("control")})
		return "", err
	default:
		return "", datapipeline.ErrInvalid
	}
}
