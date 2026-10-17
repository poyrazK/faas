package datapipelines

import (
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func verificationView(p datapipeline.Pipeline) dashboard.DataPipelineVerificationItem {
	v := p.VerificationProgress
	view := dashboard.DataPipelineVerificationItem{Restarts: v.Restarts, PipelineID: p.ID, ID: v.ID, Name: p.Name, Revision: p.Revision, State: v.State, Phase: v.Phase, Table: v.Table, Reason: v.Reason, Rows: v.Rows, Bytes: v.Bytes, Ranges: v.Ranges, DirtyRanges: v.DirtyRanges, TablesCompleted: v.TablesCompleted, TablesTotal: v.TablesTotal}
	view.BackfilledRows = v.BackfilledRows
	view.ScopeTables = v.Tables
	view.TableAddition = len(v.Tables) > 0 && len(v.Tables) < len(p.Tables)
	view.TableRepair = p.SchemaChange != nil && p.SchemaChange.State != "complete" && p.SchemaChange.Recovery != nil && p.SchemaChange.Recovery.Kind == "live"
	switch v.Reason {
	case "source_storage_changed":
		view.Reason = "A source table was rewritten. Verification will start again from its new storage version."
	case "waiting_for_replication":
		view.Reason = "Waiting for the mirror to apply source changes."
	case "waiting_for_locks":
		view.Reason = "A batch waited for a lock or timed out. Saved progress is retained."
	case "verification_row_limit":
		view.Reason = "One row exceeded the 16 MiB verification limit. Review recovery guidance."
	case "verification_range_limit":
		view.Reason = "The check reached its saved-range limit. Retry or use explicit resync."
	case "replica_mismatch":
		view.Reason = "Source and mirror differ. Review sync health and explicit recovery."
	}
	switch v.Phase {
	case "backfill":
		view.Phase = "Backfilling and verifying new columns"
	case "scan":
		view.Phase = "Scanning tables"
	case "changes":
		view.Phase = "Checking changed ranges"
	case "stopped":
		view.Phase = "Stopped"
	case "complete":
		view.Phase = "Complete"
	}
	for _, field := range []struct {
		value time.Time
		dest  *string
	}{{v.StartedAt, &view.StartedAt}, {v.UpdatedAt, &view.UpdatedAt}, {v.CompletedAt, &view.CompletedAt}} {
		if !field.value.IsZero() {
			*field.dest = field.value.UTC().Format(time.RFC3339)
		}
	}
	view.CanCancel = p.DesiredState == "running" && p.ObservationFresh && (v.State == "running" || v.State == "waiting")
	view.CanRetry = p.DesiredState == "running" && p.State == "live" && !p.VerifyRequested && (v.State == "cancelled" || v.State == "limited")
	if p.DesiredState == "paused" && v.State == "running" {
		view.State = "paused"
	}
	if p.DesiredState == "deleted" && v.State == "running" {
		view.State = "stopped"
	}
	return view
}
func (h *Handler) manageDashboardVerification(r *http.Request, account string) (string, error) {
	if r.FormValue("confirm_verification") != "yes" {
		return "", datapipeline.ErrInvalid
	}
	revision, err := strconv.ParseInt(r.FormValue("revision"), 10, 64)
	if err != nil {
		return "", datapipeline.ErrInvalid
	}
	return "", h.Service.CancelVerification(r.Context(), account, r.FormValue("pipeline_id"), revision, r.FormValue("run_id"))
}
