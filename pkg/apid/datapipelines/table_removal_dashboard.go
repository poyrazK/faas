package datapipelines

import (
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func (h *Handler) manageDashboardTableRemoval(r *http.Request, account string) (string, error) {
	revision, err := strconv.ParseInt(r.FormValue("revision"), 10, 64)
	if err != nil {
		return "", datapipeline.ErrInvalid
	}
	table, approval := r.FormValue("remove_table"), ""
	switch r.FormValue("action") {
	case "table_removal_preview":
		if table == "" {
			return "", datapipeline.ErrInvalid
		}
	case "table_removal_discard":
		table = ""
	case "table_removal_approve":
		if r.FormValue("confirm_remove_table") != "yes" || r.FormValue("cancellation_id") == "" {
			return "", datapipeline.ErrInvalid
		}
		approval = r.FormValue("cancellation_id")
	}
	err = h.Service.TableRemoval(r.Context(), account, r.FormValue("pipeline_id"), revision, table, approval)
	return "", err
}
