package datapipelines

import (
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func schemaView(p datapipeline.Pipeline) dashboard.DataPipelineSchemaChange {
	c := p.SchemaChange
	v := dashboard.DataPipelineSchemaChange{PipelineID: p.ID, Name: p.Name, Revision: p.Revision, ID: c.ID, State: c.State, Reason: p.Reason, Supported: c.Supported}
	v.TableAddition = len(c.AddedTables) > 0
	v.CanRecover = datapipeline.CanRecoverTable(p)
	v.RecoveryChoices = datapipeline.RecoverableTables(p)
	v.CancellationChoices = datapipeline.CancellableTables(p)
	if cancel := c.Cancellation; cancel != nil {
		v.TableRemoval = cancel.Kind == "live"
		v.CancellationTable, v.CancellationID, v.CancellationState, v.CancellationStage = cancel.Table, cancel.ID, cancel.State, cancel.Stage
		pendingReason := "table_cancellation_pending"
		if v.TableRemoval {
			pendingReason = "table_removal_pending"
		}
		v.CanApproveCancellation = cancel.State == "pending" && p.Reason == pendingReason && p.State == "blocked" && p.DesiredState == "running" && p.ObservationFresh
		v.CanDiscardCancellation = (cancel.State == "requested" || cancel.State == "pending") && p.DesiredState == "running"
		if cancel.State == "withdrawn" {
			v.CancellationTable = ""
		}
	}
	if v.CanRecover {
		v.RecoveryTable = v.RecoveryChoices[0]
	}
	if c.Recovery != nil {
		v.RecoveryTable, v.RecoveryID, v.RecoveryState, v.RecoveryStage = c.Recovery.Table, c.Recovery.ID, c.Recovery.State, c.Recovery.Stage
		v.CanApproveRecovery = c.Recovery.State == "pending" && p.Reason == "table_recovery_pending" && p.State == "blocked" && p.DesiredState == "running" && p.ObservationFresh
		if c.Recovery.Kind == "live" {
			v.TableAddition = false
		}
	}
	for _, copy := range c.Copies {
		v.Copies = append(v.Copies, dashboard.DataPipelineTableCopy{Table: copy.Table, Phase: copy.Phase, Rows: copy.Rows, Bytes: copy.Bytes, WorkerActive: copy.WorkerActive, CountersKnown: copy.CountersKnown, Stalled: copy.Stalled, UpdatedAt: copy.ObservedAt.Format(time.RFC3339)})
	}
	if p.Health != nil {
		v.Reason = p.Health.Message + " " + p.Health.Recovery
	}
	switch c.State {
	case "complete":
		v.Reason = "Approved columns are available. Regenerate client types before using the new fields."
		if v.TableAddition {
			v.Reason = "Approved tables are available. Regenerate client types before querying them."
		}
		if c.Recovery != nil && c.Recovery.Kind == "live" {
			v.Reason = "The repaired table is available after a fresh copy and verification. Other tables kept syncing."
		}
		if c.Cancellation != nil && c.Cancellation.State == "complete" && !v.TableAddition {
			v.Reason = "Table onboarding was cancelled. Existing tables remain available and imported rows are retained privately."
			if v.TableRemoval {
				v.Reason = "The table was removed. Remaining tables keep syncing and imported rows are retained privately."
			}
		}
	case "withdrawn":
		v.Reason = "The source returned to its previous definition before application. The pending approval was withdrawn."
	}
	if c.Cancellation != nil && c.Cancellation.State == "withdrawn" {
		v.Reason = "The table completed onboarding before cancellation started. It remains available."
	}
	v.CanApprove = c.State == "pending" && c.Supported && p.DesiredState == "running" && p.State == "blocked" && p.ObservationFresh
	if v.TableAddition && p.State == "live" && p.Reason == "table_addition_pending" && c.State == "pending" && c.Supported && p.DesiredState == "running" && p.ObservationFresh {
		v.CanApprove = true
	}
	v.CanRetry = c.Supported && p.SchemaApproval == c.ID && (c.State == "verifying" || c.State == "refreshing") && p.Reason != "schema_api_refresh_pending" && p.State == "blocked" && p.ObservationFresh && c.Retry < api.DataPipelineSchemaRetriesMax
	if p.VerificationProgress != nil && p.VerificationProgress.State == "running" && p.Reason != "schema_api_refresh_failed" && p.Reason != "schema_api_refresh_unavailable" {
		v.CanRetry = false
	}
	if c.Recovery != nil && c.Recovery.State != "complete" && c.Recovery.Stage != "verifying" {
		v.CanRetry = false
	}
	if c.Cancellation != nil && c.Cancellation.State != "complete" {
		v.CanApprove, v.CanRetry, v.CanApproveRecovery = false, false, false
	}
	if p.ReaderChange != nil && p.ReaderChange.State == "approved" {
		v.CanApprove, v.CanRetry = false, false
	}
	for _, d := range c.Changes {
		v.Changes = append(v.Changes, dashboard.DataPipelineSchemaColumn{Table: d.Table, Column: d.Column, Kind: d.Kind, BeforeType: d.BeforeType, AfterType: d.AfterType, Reason: d.Reason, Safe: d.Safe})
	}
	for _, r := range c.Refreshes {
		v.Refreshes = append(v.Refreshes, dashboard.DataPipelineSchemaRefresh{AppID: r.AppID, Slug: r.Slug, WakeID: r.WakeID, Status: r.Status})
	}
	return v
}

func (h *Handler) manageDashboardSchema(r *http.Request, account string) (string, error) {
	if r.FormValue("confirm_schema") != "yes" {
		return "", datapipeline.ErrInvalid
	}
	revision, err := strconv.ParseInt(r.FormValue("revision"), 10, 64)
	if err != nil {
		return "", datapipeline.ErrInvalid
	}
	err = h.Service.ApproveSchema(r.Context(), account, r.FormValue("pipeline_id"), revision, r.FormValue("change_id"), r.FormValue("action") == "schema_retry")
	return "", err
}
