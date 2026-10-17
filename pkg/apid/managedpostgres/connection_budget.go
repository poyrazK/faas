package managedpostgres

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	domain "github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

type ConnectionBudgetHandler struct {
	Service *domain.BindingService
	Problem func(http.ResponseWriter, error)
}

func (h ConnectionBudgetHandler) Database(w http.ResponseWriter, r *http.Request, account state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if h.Service == nil {
		h.Problem(w, domain.ErrUnavailable)
		return
	}
	if _, err := uuid.Parse(r.PathValue("id")); err != nil {
		h.Problem(w, domain.ErrInvalid)
		return
	}
	report, err := h.Service.ConnectionBudget(r.Context(), account.ID, r.PathValue("id"))
	if err != nil {
		h.Problem(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, report)
}

func (h ConnectionBudgetHandler) App(w http.ResponseWriter, r *http.Request, account state.Account, app state.App) {
	w.Header().Set("Cache-Control", "no-store")
	if h.Service == nil {
		h.Problem(w, domain.ErrUnavailable)
		return
	}
	if app.AccountID != account.ID {
		h.Problem(w, domain.ErrNotFound)
		return
	}
	report, err := h.Service.AppConnectionBudgets(r.Context(), account.ID, app.ID, app.Slug)
	if err != nil {
		h.Problem(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, report)
}
