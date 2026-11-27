package apidserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) setManagedPostgresMaintenance(w http.ResponseWriter, r *http.Request, account state.Account) {
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	var request struct {
		Window json.RawMessage `json:"window"`
	}
	if err := decodeJSON(r, &request); err != nil || len(request.Window) == 0 {
		managedPostgresProblem(w, fmt.Errorf("%w: window is required. Use null to remove the schedule", managedpostgres.ErrInvalid))
		return
	}
	var window *api.ManagedPostgresMaintenanceWindow
	if !bytes.Equal(request.Window, []byte("null")) {
		var fields struct {
			Weekday         *int `json:"weekday"`
			StartMinute     *int `json:"start_minute"`
			DurationMinutes *int `json:"duration_minutes"`
		}
		if err := json.Unmarshal(request.Window, &fields); err != nil ||
			fields.Weekday == nil || fields.StartMinute == nil || fields.DurationMinutes == nil {
			managedPostgresProblem(w, fmt.Errorf("%w: window requires weekday, start_minute, and duration_minutes", managedpostgres.ErrInvalid))
			return
		}
		window = &api.ManagedPostgresMaintenanceWindow{
			Weekday: *fields.Weekday, StartMinute: *fields.StartMinute, DurationMinutes: *fields.DurationMinutes,
		}
	}
	result, err := s.managedPostgres.SetMaintenanceWindow(r.Context(), account.ID, r.PathValue("id"), window)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}
