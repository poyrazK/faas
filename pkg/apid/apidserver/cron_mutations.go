package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/cronmutation"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) cronMutationDeps(deletion bool) cronmutation.Deps {
	store, _ := s.store.(state.CronMutationStore)
	return cronmutation.Deps{Store: store, Deletion: deletion,
		Resolve: func(w http.ResponseWriter, r *http.Request, account state.Account) (state.App, bool) {
			return s.loadApp(w, r, account, r.PathValue("slug"))
		}, Validate: validateCronMutation, After: s.afterCronMutation}
}

func validateCronMutation(request *api.CronMutationRequest) *api.Problem {
	if request.Schedule != nil {
		if p := cronScheduleProblem(*request.Schedule); p != nil {
			return p
		}
	}
	if request.Path != nil {
		if p := api.ValidateCronPath(*request.Path); p != nil {
			return p
		}
	}
	if request.Timezone != nil {
		tz, err := normalizeCronTimezone(*request.Timezone)
		if err != nil {
			return api.ErrCronInvalid("timezone must be an IANA location")
		}
		// Retried inputs keep an identical request fingerprint.
		request.Timezone = &tz
	}
	return nil
}

func (s *server) afterCronMutation(r *http.Request, account state.Account, out api.CronMutationReceipt) {
	_ = s.notif.Notify(r.Context(), db.NotifyCronChanged, `{"kind":"reviewed","cron":"`+out.CronID+`"}`)
	s.audit.Emit(r.Context(), "cron.mutation", &account.ID, map[string]any{
		"request_id": out.RequestID, "cron_id": out.CronID, "app_id": out.AppID, "status": out.Status, "phase": out.Phase})
}
