package managedrealtime

import (
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// RealtimeUsage serves GET /v1/usage/realtime: the account's open connections
// against its plan's fair-use limit, and recorded usage per hour (ADR-1323).
func (s *Service) RealtimeUsage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.UsageEnabled {
		httpkit.NotFound(w, "realtime usage unavailable")
		return
	}
	hours, ok := realtimeUsageHours(w, r)
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.RealtimeAccountUsageReader)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("realtime usage store unavailable"))
		return
	}
	to := time.Now().UTC().Truncate(time.Hour).Add(time.Hour)
	from := to.Add(-time.Duration(hours) * time.Hour)
	open, err := store.ReadRealtimeOpenConnections(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("realtime usage unavailable"))
		return
	}
	account, err := store.ReadRealtimeAccountUsage(r.Context(), acct.ID, from, to)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("realtime usage unavailable"))
		return
	}
	endpoints, err := store.ReadRealtimeUsage(r.Context(), acct.ID, from, to)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("realtime usage unavailable"))
		return
	}
	limits, _ := api.RealtimeLimitsFor(acct.Plan)
	out := api.RealtimeUsageResponse{Plan: acct.Plan, ConnectionLimit: limits.ConnectionsPerAccount, OpenConnections: open,
		From: from, To: to, Hours: make([]api.RealtimeUsageHour, 0, len(account)), Endpoints: make([]api.RealtimeEndpointUsageHour, 0, len(endpoints))}
	for _, h := range account {
		out.Hours = append(out.Hours, api.RealtimeUsageHour(h))
	}
	for _, e := range endpoints {
		out.Endpoints = append(out.Endpoints, api.RealtimeEndpointUsageHour{EndpointID: e.EndpointID, AppID: e.AppID, RealtimeUsageHour: api.RealtimeUsageHour{
			Hour: e.Hour, PeakConnections: e.PeakConnections, ConnectionSeconds: e.ConnectionSeconds, DeliveredMessages: e.DeliveredMessages,
			DeliveredBytes: e.DeliveredBytes, ReceivedMessages: e.ReceivedMessages, ReceivedBytes: e.ReceivedBytes,
		}})
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// realtimeUsageHours parses ?hours=, defaulting to the last day.
func realtimeUsageHours(w http.ResponseWriter, r *http.Request) (int, bool) {
	values := r.URL.Query()["hours"]
	if len(values) == 0 {
		return api.RealtimeUsageQueryDefaultHours, true
	}
	hours, err := strconv.Atoi(values[0])
	if len(values) != 1 || err != nil || hours < 1 || hours > api.RealtimeUsageQueryMaxHours {
		api.WriteProblem(w, api.ErrRealtimeInvalid("hours must be a whole number from 1 to 744").
			WithLimit(api.RealtimeUsageQueryMaxHours, int64(hours)))
		return 0, false
	}
	return hours, true
}
