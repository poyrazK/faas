package datapipelines

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func setupView(s datapipeline.Setup) dashboard.DataPipelineSetupItem {
	v := dashboard.DataPipelineSetupItem{ID: s.ID, Name: s.Request.Name, ConnectionID: s.Request.SourceConnectionID, DatabaseID: s.DatabaseID, PipelineID: s.PipelineID, State: s.State, Phase: s.Phase, Revision: s.Revision, Tables: s.Request.Tables, UpdatedAt: s.UpdatedAt, DataAPIName: s.Request.DataAPIName, DataAPIAppID: s.DataAPIPlan.AppID, DeploymentID: s.DataAPIPlan.DeploymentID, Endpoint: s.DataAPIPlan.Endpoint}
	switch s.Phase {
	case "checking_source":
		v.Progress = "Checking selected source tables"
	case "provisioning":
		v.Progress = "Preparing your managed mirror"
	case "checking_mirror":
		v.Progress = "Checking mirror compatibility and capacity"
	case "starting":
		v.Progress = "Starting replication"
	case "configuring_api":
		v.Progress = "Configuring your read-only Data API"
	case "deploying_api":
		v.Progress = "Submitting the Data API build"
	case "waiting_api":
		v.Progress = "Building and deploying the Data API"
	case "api_failed":
		v.Progress = "Data API deployment failed"
	case "syncing":
		v.Progress = "Copying tables and catching up"
	}
	switch s.State {
	case "ready":
		v.Progress = "Your mirror is live"
		v.Remediation = "You can now configure a read-only Data API using the mirror ID below."
		if s.Request.DataAPIName != "" {
			v.Progress = "Your mirror and read-only Data API are ready"
			v.Remediation = "Use a user JWT from the selected Auth app. Only the saved read subjects can query these tables."
		}
	case "cancelled":
		v.Progress = "Setup stopped"
		v.Remediation = "Requested databases and pipelines are retained. Manage or delete them separately."
	case "blocked":
		v.Progress += " — needs attention"
		switch s.Reason {
		case "capacity_needs_qualification":
			v.Remediation = "This mirror's allocation needs operator qualification. Once it is qualified, retry this saved setup."
		case "insufficient_capacity":
			v.Remediation = "Free capacity or choose a larger qualified allocation, then retry."
		case "connection_check_blocked", "connection_check_required":
			v.Remediation = "Review the connection check, fix the source requirements or rotate its credential, then retry."
		case "resource_conflict":
			v.Remediation = "A saved resource conflicts with this setup. Inspect its mirror and pipeline before retrying."
		case "account_limit":
			v.Remediation = "Your account has reached a resource limit. Remove unused resources before retrying."
		case "setup_configuration_invalid":
			v.Remediation = "The saved mirror configuration is unavailable for the current account or placement. Review its plan and region."
		case "data_api_deployment_failed":
			v.Remediation = "Review deployment logs and fix the reported issue, then retry. Your live mirror, app and read-only binding are retained."
		case "pipeline_needs_attention":
			v.Remediation = "Review sync health below. Resume or recover the pipeline, then retry this setup."
		default:
			v.Remediation = "Check service availability and the saved resources below, then retry. Your selections and resource IDs are retained."
		}
	case "working":
		updated, err := time.Parse(time.RFC3339Nano, s.UpdatedAt)
		if err == nil && time.Since(updated) > api.DataPipelineObservationMaxAge {
			v.Remediation = "Waiting for the setup worker. Your selections are saved."
		}
	}
	return v
}

func (h *Handler) manageDashboardConnection(r *http.Request, account string) (string, error) {
	ctx, id := r.Context(), r.FormValue("connection_id")
	switch r.FormValue("action") {
	case "rotate":
		revision, err := strconv.ParseInt(r.FormValue("revision"), 10, 64)
		if err != nil {
			return "", datapipeline.ErrInvalid
		}
		_, err = h.Service.RotateConnection(ctx, account, id, api.RotateDataConnectionRequest{ExpectedRevision: revision, ConnectionURL: r.FormValue("connection_url")})
		return id, err
	case "delete_connection":
		if r.FormValue("confirm_delete") != "yes" {
			return "", datapipeline.ErrInvalid
		}
		return "", h.Service.Store.DeleteConnection(ctx, account, id)

	}
	return "", datapipeline.ErrInvalid
}
func (h *Handler) manageDashboardSetup(r *http.Request, account string) (string, error) {
	ctx, id := r.Context(), r.FormValue("connection_id")
	switch r.FormValue("action") {
	case "setup":
		subjects := strings.Split(r.FormValue("read_subjects"), ",")
		for i := range subjects {
			subjects[i] = strings.TrimSpace(subjects[i])
		}
		_, err := h.Service.StartSetup(ctx, account, datapipeline.SetupRequest{Name: r.FormValue("name"), SourceConnectionID: id, DatabaseID: r.FormValue("database_id"), Region: r.FormValue("region"), Tables: r.Form["tables"], ReadSubjects: subjects, DataAPIName: r.FormValue("data_api_name"), AuthAppID: r.FormValue("auth_app_id")})
		return id, err
	case "setup_control":
		if r.FormValue("control") == "retry" && !h.Service.Available(ctx, account) {
			return "", datapipeline.ErrUnavailable
		}
		if r.FormValue("control") == "cancel" && r.FormValue("confirm_cancel") != "yes" {
			return "", datapipeline.ErrInvalid
		}
		revision, err := strconv.ParseInt(r.FormValue("revision"), 10, 64)
		if err != nil {
			return "", datapipeline.ErrInvalid
		}
		store, ok := h.Service.Store.(datapipeline.SetupStore)
		if !ok {
			return "", datapipeline.ErrUnavailable
		}
		updated, err := store.ControlSetup(ctx, account, r.FormValue("setup_id"), revision, r.FormValue("control"))
		return updated.Request.SourceConnectionID, err

	}
	return "", datapipeline.ErrInvalid
}
