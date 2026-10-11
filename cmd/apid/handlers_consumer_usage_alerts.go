package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// apiConsumerUsageAlertListLimit bounds one usage alert listing; a consumer
// records at most five alerts per plan per month (ADR-849).
const apiConsumerUsageAlertListLimit = 100

// listAPIConsumerUsageAlerts returns the plan alert thresholds a consumer
// crossed, newest first, so a missed webhook can be reconciled.
func (s *server) listAPIConsumerUsageAlerts(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.apiConsumerPlanStore(w, r, acct)
	if !ok {
		return
	}
	consumer, err := s.store.GetAPIConsumerByID(r.Context(), acct.ID, r.PathValue("consumer_id"))
	if err != nil || consumer.AppID != app.ID {
		s.notFound(w, "no such consumer")
		return
	}
	alerts, err := store.ListAPIConsumerUsageAlerts(r.Context(), acct.ID, app.ID, consumer.ID, apiConsumerUsageAlertListLimit)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list consumer usage alerts"))
		return
	}
	out := api.APIConsumerUsageAlertListResponse{Alerts: make([]api.APIConsumerUsageAlertResponse, 0, len(alerts))}
	for _, alert := range alerts {
		out.Alerts = append(out.Alerts, api.APIConsumerUsageAlertResponse{
			ID: alert.ID, ConsumerID: alert.ConsumerID, PlanID: alert.PlanID, MonthStart: alert.MonthStart.UTC(),
			ThresholdPercent: alert.ThresholdPercent, LimitUnits: alert.LimitUnits, UsedUnits: alert.UsedUnits,
			CrossedAt: alert.CrossedAt.UTC(),
		})
	}
	writeJSON(w, http.StatusOK, out)
}
