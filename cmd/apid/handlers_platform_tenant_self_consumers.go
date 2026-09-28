package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) createPlatformTenantSelfConsumer(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenantID, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	if !s.consumerFeatureAllowed(w, acct) {
		return
	}
	store, ok := s.store.(state.PlatformTenantSelfConsumerProvisioningStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant customer provisioning is unavailable"))
		return
	}
	var req api.CreatePlatformTenantSelfConsumerRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if _, err := uuid.Parse(req.SurfaceID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid surface", "surface_id must be a UUID"))
		return
	}
	input := state.CreatePlatformTenantSelfConsumerParams{AccountID: acct.ID, ExternalRef: strings.TrimSpace(req.ExternalRef),
		Name: strings.TrimSpace(req.Name), SurfaceID: req.SurfaceID}
	if input.ExternalRef == "" || len(input.ExternalRef) > 256 || input.Name == "" || len(input.Name) > 128 {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid customer identity", "external_ref must be 1-256 characters and name must be 1-128 characters"))
		return
	}
	input.TenantID = tenantID
	consumer, created, err := store.CreatePlatformTenantSelfConsumer(r.Context(), input)
	if err != nil {
		s.platformTenantSelfConsumerError(w, err)
		return
	}
	if created {
		s.audit.Emit(r.Context(), "platform_tenant.self_consumer_created", &acct.ID, map[string]any{
			"tenant_id": tenantID, "consumer_id": consumer.ID, "surface_id": input.SurfaceID,
		})
	}
	w.Header().Set("Cache-Control", "no-store")
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, api.PlatformTenantSelfConsumerResponse{ConsumerID: consumer.ID, ExternalRef: consumer.ExternalRef,
		Name: consumer.Name, Status: string(consumer.Status)})
}

func (s *server) platformTenantSelfConsumerError(w http.ResponseWriter, err error) {
	var quota *state.PlatformTenantConsumerProvisioningQuotaError
	switch {
	case errors.Is(err, state.ErrPlatformTenantConsumerProvisioningDisabled):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "platform_tenant_consumer_provisioning_disabled",
			"Customer provisioning is disabled", "ask the platform owner to enable customer provisioning for this tenant"))
	case errors.As(err, &quota):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "platform_tenant_consumer_limit_reached",
			"Customer limit reached", "ask the platform owner to increase this tenant's active-customer limit"))
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "no active surface linked to this platform tenant")
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "platform_tenant_consumer_conflict",
			"Customer identity conflict", "the tenant or surface is inactive, or this app already has a different identity with that external_ref"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid customer identity", "surface_id, external_ref, and name must be valid"))
	default:
		api.WriteProblem(w, api.ErrInternal("could not create tenant customer identity"))
	}
}
