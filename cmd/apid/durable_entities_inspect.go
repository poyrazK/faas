// adr: 935
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) inspectDurableEntity(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "private, no-store")
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if s.durableEntities == nil || !s.durableEntityApps[app.ID] {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "durable_entities_unavailable", "Durable entities unavailable", "this app is not enabled for the durable entity preview"))
		return
	}
	request, err := durableEntityInspectionQuery(r.URL.RawQuery)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("inspection requires namespace and key and accepts only one value per supported selector"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.DurableEntityInvokeTimeout)
	defer cancel()
	id, _, problem := s.resolveDurableEntityIdentity(r.WithContext(ctx), acct, app, api.DurableEntityInvokeRequest{Namespace: request.Namespace, Key: request.Key, Environment: request.Environment, PlatformTenantID: request.PlatformTenantID}, true)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	inspection, err := s.durableEntities.Inspect(ctx, id)
	if errors.Is(err, durableentity.ErrNotFound) && !errors.Is(err, durableentity.ErrCorrupt) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Entity not found", "no entity exists in the selected scope"))
		return
	}
	if err != nil {
		writeDurableEntityProblem(w, err)
		return
	}
	out := durableEntityInspectionDTO(id, inspection)
	out.Outbox.HeadDelivery = s.durableEntityHeadDelivery(ctx, id, inspection.Outbox.HeadID)
	writeJSON(w, http.StatusOK, out)
}

func durableEntityInspectionQuery(raw string) (api.DurableEntityInspectRequest, error) {
	query, err := url.ParseQuery(raw)
	if err != nil {
		return api.DurableEntityInspectRequest{}, durableentity.ErrInvalid
	}
	for key, values := range query {
		if len(values) != 1 || values[0] == "" || key != "namespace" && key != "key" && key != "environment" && key != "platform_tenant_id" {
			return api.DurableEntityInspectRequest{}, durableentity.ErrInvalid
		}
	}
	request := api.DurableEntityInspectRequest{Namespace: query.Get("namespace"), Key: query.Get("key"), Environment: query.Get("environment"), PlatformTenantID: query.Get("platform_tenant_id")}
	if request.Namespace == "" || request.Key == "" {
		return request, durableentity.ErrInvalid
	}
	return request, nil
}

func durableEntityInspectionDTO(id durableentity.ID, in durableentity.Inspection) api.DurableEntityInspectResponse {
	out := api.DurableEntityInspectResponse{
		Entity:  api.DurableEntityScope{AccountID: id.AccountID, AppID: id.AppID, EnvironmentID: id.EnvironmentID, TenantID: id.TenantID, Namespace: id.Namespace, Key: id.Key},
		Version: in.Version, RecoveryRevision: in.RecoveryRevision, StateCommitted: in.Version > 0,
		Alarm:  api.DurableEntityAlarmInspection{Attempts: in.Alarm.Attempts, NextAttemptAt: in.Alarm.NextAttemptAt, Exhausted: in.Alarm.Exhausted},
		Outbox: api.DurableEntityOutboxInspection{Pending: in.Outbox.Pending, HeadID: in.Outbox.HeadID, Attempts: in.Outbox.Attempts, NextAttemptAt: in.Outbox.NextAttemptAt, Exhausted: in.Outbox.Exhausted},
	}
	if in.Alarm.Alarm != nil {
		at := in.Alarm.Alarm.At
		out.Alarm.AlarmAt = &at
	}
	return out
}

func (s *server) durableEntityHeadDelivery(ctx context.Context, id durableentity.ID, headID string) *api.DurableEntityHeadDelivery {
	if headID == "" {
		return nil
	}
	out := &api.DurableEntityHeadDelivery{Status: "unknown"}
	row, err := s.store.AppWebhookDeliveryByID(ctx, headID)
	if err != nil || row.ID != headID || row.AccountID != id.AccountID || row.AppID != id.AppID {
		return out
	}
	var body struct {
		Entity    durableentity.ID `json:"entity"`
		MessageID string           `json:"message_id"`
	}
	if json.Unmarshal(row.Payload, &body) != nil || body.Entity != id || body.MessageID != headID {
		return out
	}
	switch row.Status {
	case state.AppWebhookDeliveryPending, state.AppWebhookDeliveryInFlight, state.AppWebhookDeliverySucceeded, state.AppWebhookDeliveryFailed, state.AppWebhookDeliveryDead:
		out.Status = string(row.Status)
	}
	return out
}
