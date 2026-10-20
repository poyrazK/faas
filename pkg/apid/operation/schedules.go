// adr: 20261018161833
package operation

import (
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// scheduleStore returns the schedule store when schedules are open to the
// account. A closed account sees the routes as absent.
func (s *Service) scheduleStore(w http.ResponseWriter, acct state.Account) (state.OperationScheduleStore, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if !api.OperationSchedulesEnabled(acct.ID) {
		httpkit.NoSuchRoute(w)
		return nil, false
	}
	store, ok := s.Host.Store().(state.OperationScheduleStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("operation schedule storage is unavailable"))
	}
	return store, ok
}

func writeScheduleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Schedule not found", "the schedule, its definition or its customer is unavailable"))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "operation_schedule_state_conflict", "Schedule state changed", "only an active cron schedule pauses, only a paused one resumes, and a settled schedule takes no changes"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("set at (in the future, at most 366 days ahead) or cron (five fields) with an optional IANA timezone; input must match the definition's input schema"))
	default:
		WriteOperationError(w, err)
	}
}

func scheduleResponses(in []state.OperationSchedule) api.OperationScheduleListResponse {
	out := api.OperationScheduleListResponse{Schedules: make([]api.OperationScheduleResponse, 0, len(in))}
	for _, s := range in {
		out.Schedules = append(out.Schedules, s.OperationScheduleResponse)
	}
	return out
}

// CreatePlatformTenantSelfOperationSchedule lets a customer schedule its own
// Operations. The schedule fires under the same admission rules as a direct
// start, checked again at every fire.
func (s *Service) CreatePlatformTenantSelfOperationSchedule(w http.ResponseWriter, r *http.Request, acct state.Account) {
	schedules, ok := s.scheduleStore(w, acct)
	if !ok {
		return
	}
	operations, ok := s.OperationStore(w)
	if !ok {
		return
	}
	tenant, ok := httpkit.PlatformTenantSelfID(w, r)
	if !ok {
		return
	}
	var req api.OperationScheduleRequest
	if !DecodeOperationBody(w, r, &req, api.OperationScheduleBodyMaxBytes) {
		return
	}
	def, err := operations.OperationDefinitionByID(r.Context(), acct.ID, req.DefinitionID)
	if err != nil {
		writeScheduleError(w, err)
		return
	}
	if !s.operationAdmissionOpen(w, acct.ID, def.AppID, def.Scope, tenant, def.Spec) {
		return
	}
	schedule, err := schedules.CreateOperationSchedule(r.Context(), state.OperationScheduleInput{AccountID: acct.ID, PlatformTenantID: tenant, Request: req, Now: time.Now()})
	if err != nil {
		writeScheduleError(w, err)
		return
	}
	s.Host.EmitAudit(r.Context(), "app.operation_schedule_created", &acct.ID, map[string]any{"schedule_id": schedule.ID, "platform_tenant_id": tenant, "definition_id": schedule.DefinitionID, "kind": schedule.Kind})
	httpkit.WriteJSON(w, http.StatusCreated, schedule.OperationScheduleResponse)
}

func (s *Service) ListPlatformTenantSelfOperationSchedules(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.scheduleStore(w, acct)
	if !ok {
		return
	}
	tenant, ok := s.activePlatformTenantSelfID(w, r, acct)
	if !ok {
		return
	}
	list, err := store.ListOperationSchedules(r.Context(), acct.ID, tenant, "")
	if err != nil {
		writeScheduleError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, scheduleResponses(list))
}

func (s *Service) GetPlatformTenantSelfOperationSchedule(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.scheduleStore(w, acct)
	if !ok {
		return
	}
	tenant, ok := s.activePlatformTenantSelfID(w, r, acct)
	if !ok {
		return
	}
	schedule, err := store.OperationSchedule(r.Context(), acct.ID, tenant, r.PathValue("id"))
	if err != nil {
		writeScheduleError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, schedule.OperationScheduleResponse)
}

// ChangePlatformTenantSelfOperationSchedule returns the handler that applies
// one action (pause, resume, cancel) to the customer's own schedule.
func (s *Service) ChangePlatformTenantSelfOperationSchedule(action string) func(http.ResponseWriter, *http.Request, state.Account) {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		store, ok := s.scheduleStore(w, acct)
		if !ok {
			return
		}
		tenant, ok := httpkit.PlatformTenantSelfID(w, r)
		if !ok {
			return
		}
		s.changeSchedule(w, r, acct, store, tenant, r.PathValue("id"), action)
	}
}

func (s *Service) changeSchedule(w http.ResponseWriter, r *http.Request, acct state.Account, store state.OperationScheduleStore, tenant, id, action string) {
	schedule, err := store.ChangeOperationSchedule(r.Context(), acct.ID, tenant, id, action, time.Now())
	if err != nil {
		writeScheduleError(w, err)
		return
	}
	source := api.OperationSignalSourceCustomer
	if tenant == "" {
		source = api.OperationSignalSourceAccount
	}
	s.Host.EmitAudit(r.Context(), "app.operation_schedule_"+action, &acct.ID, map[string]any{"schedule_id": schedule.ID, "platform_tenant_id": schedule.PlatformTenantID, "source": source})
	httpkit.WriteJSON(w, http.StatusOK, schedule.OperationScheduleResponse)
}

// ListAppOperationSchedules lists every customer's schedules on one app.
func (s *Service) ListAppOperationSchedules(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.scheduleStore(w, acct)
	if !ok {
		return
	}
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	list, err := store.ListOperationSchedules(r.Context(), acct.ID, r.URL.Query().Get("platform_tenant_id"), app.ID)
	if err != nil {
		writeScheduleError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, scheduleResponses(list))
}

// CancelAppOperationSchedule lets the account stop any customer's schedule
// on its app. Operations a schedule already admitted are unaffected.
func (s *Service) CancelAppOperationSchedule(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.scheduleStore(w, acct)
	if !ok {
		return
	}
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	schedule, err := store.OperationSchedule(r.Context(), acct.ID, "", r.PathValue("id"))
	if err == nil && schedule.AppID != app.ID {
		err = state.ErrNotFound
	}
	if err != nil {
		writeScheduleError(w, err)
		return
	}
	s.changeSchedule(w, r, acct, store, "", schedule.ID, state.OperationScheduleCancel)
}
