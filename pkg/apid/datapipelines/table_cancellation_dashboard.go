package datapipelines

import (
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func (h *Handler) manageDashboardTableCancellation(r *http.Request, account string) (string, error) {
	revision, err := strconv.ParseInt(r.FormValue("revision"), 10, 64)
	if err != nil {
		return "", datapipeline.ErrInvalid
	}
	approval := ""
	table := r.FormValue("cancel_table")
	if r.FormValue("action") == "table_cancellation_preview" && table == "" {
		return "", datapipeline.ErrInvalid
	}
	if r.FormValue("action") == "table_cancellation_discard" {
		table = ""
	}
	if r.FormValue("action") == "table_cancellation_approve" {
		if r.FormValue("confirm_cancel_table") != "yes" || r.FormValue("cancellation_id") == "" {
			return "", datapipeline.ErrInvalid
		}
		approval = r.FormValue("cancellation_id")
	}
	err = h.Service.TableCancellation(r.Context(), account, r.FormValue("pipeline_id"), revision, table, approval)
	return "", err
}
