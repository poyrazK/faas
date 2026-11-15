package apidserver

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) obsNodeProbeNotifications(w http.ResponseWriter, r *http.Request, caller state.Account) {
	if allowed, prob := s.adminAllows(caller); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	selected, err := operatorobs.NodeProbeNotificationsSelection(r.PathValue("name"), r.URL.RawQuery, r.URL.ForceQuery)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid node notification selection", "requires a stable node name and no query parameters"))
		return
	}
	writeJSON(w, http.StatusOK, operatorobs.NodeProbeNotifications(r.Context(), s.promqlClient, selected, time.Now().UTC()))
}
