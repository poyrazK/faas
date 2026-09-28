package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) applyPlatformTenantSelfConsumers(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenantID, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	if !s.consumerFeatureAllowed(w, acct) {
		return
	}
	store, ok := s.store.(state.PlatformTenantSelfConsumerApplyStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant customer onboarding is unavailable"))
		return
	}
	var req api.ApplyPlatformTenantSelfConsumersRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	input := state.ApplyPlatformTenantSelfConsumersParams{
		AccountID: acct.ID, TenantID: tenantID, ExternalRef: strings.TrimSpace(req.ExternalRef),
		Name: strings.TrimSpace(req.Name), SurfaceIDs: req.SurfaceIDs, DryRun: req.DryRun,
	}
	result, err := store.ApplyPlatformTenantSelfConsumers(r.Context(), input)
	if err != nil {
		if errors.Is(err, state.ErrInvalidArgument) {
			api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
				"Invalid customer batch", "surface_ids must contain 1-100 unique UUIDs with at most one surface per app; external_ref and name must be valid"))
			return
		}
		s.platformTenantSelfConsumerError(w, err)
		return
	}
	response := api.ApplyPlatformTenantSelfConsumersResponse{
		DryRun: req.DryRun, Consumers: make([]api.PlatformTenantSelfConsumerApplyItemResponse, 0, len(result.Consumers)),
	}
	createdIDs := make([]string, 0, len(result.Consumers))
	createdSurfaces := make([]string, 0, len(result.Consumers))
	for _, item := range result.Consumers {
		action := "unchanged"
		if item.Created {
			action = "create"
			if !req.DryRun {
				createdIDs = append(createdIDs, item.Consumer.ID)
				createdSurfaces = append(createdSurfaces, item.SurfaceID)
			}
		}
		response.Consumers = append(response.Consumers, api.PlatformTenantSelfConsumerApplyItemResponse{
			SurfaceID: item.SurfaceID, ConsumerID: item.Consumer.ID, ExternalRef: item.Consumer.ExternalRef,
			Name: item.Consumer.Name, Status: string(item.Consumer.Status), Action: action,
		})
	}
	if result.Changed && !req.DryRun {
		s.audit.Emit(r.Context(), "platform_tenant.self_consumers_applied", &acct.ID, map[string]any{
			"tenant_id": tenantID, "surface_ids": createdSurfaces, "consumer_ids": createdIDs,
			"created_count": len(createdIDs),
		})
	}
	w.Header().Set("Cache-Control", "no-store")
	status := http.StatusOK
	if result.Changed && !req.DryRun {
		status = http.StatusCreated
	}
	writeJSON(w, status, response)
}
