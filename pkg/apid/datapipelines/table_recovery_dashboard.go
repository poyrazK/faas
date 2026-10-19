package datapipelines

import (
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func (h *Handler) manageDashboardTableRecovery(r *http.Request, account string) (string, error) {
	revision, err := strconv.ParseInt(r.FormValue("revision"), 10, 64)
	if err != nil {
		return "", datapipeline.ErrInvalid
	}
	approval := ""
	if r.FormValue("action") == "table_recovery_approve" {
		if r.FormValue("confirm_recopy") != "yes" || r.FormValue("recovery_id") == "" {
			return "", datapipeline.ErrInvalid
		}
		approval = r.FormValue("recovery_id")
	}
	err = h.Service.TableRecovery(r.Context(), account, r.FormValue("pipeline_id"), revision, approval, r.FormValue("copy_table"))
	return "", err
}
