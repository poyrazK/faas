package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) revokePlatformTenantSelfConsumers(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenantID, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	store, ok := s.store.(state.PlatformTenantSelfConsumerRevocationStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant customer revocation is unavailable"))
		return
	}
	var req api.RevokePlatformTenantSelfConsumersRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	result, err := store.RevokePlatformTenantSelfConsumers(r.Context(), state.RevokePlatformTenantSelfConsumersParams{
		AccountID: acct.ID, TenantID: tenantID, ConsumerIDs: req.ConsumerIDs,
	})
	switch {
	case errors.Is(err, state.ErrNotFound):
		// Keep individual and mixed-tenant IDs indistinguishable to avoid making
		// consumer existence or ownership enumerable across tenant boundaries.
		s.notFound(w, "one or more customers are not managed by this tenant")
		return
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid customer batch", "consumer_ids must contain 1-100 unique UUIDs"))
		return
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("could not revoke tenant customer access"))
		return
	}

	consumers := make([]api.PlatformTenantSelfConsumerResponse, 0, len(result.Consumers))
	consumerIDs := make([]string, 0, len(result.Consumers))
	for _, consumer := range result.Consumers {
		consumers = append(consumers, api.PlatformTenantSelfConsumerResponse{
			ConsumerID: consumer.ID, ExternalRef: consumer.ExternalRef, Name: consumer.Name, Status: string(consumer.Status),
		})
		consumerIDs = append(consumerIDs, consumer.ID)
	}
	if result.Changed {
		s.audit.Emit(r.Context(), "platform_tenant.self_consumers_revoked", &acct.ID, map[string]any{
			"tenant_id": tenantID, "consumer_ids": consumerIDs, "revoked_key_count": result.RevokedKeys,
		})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, api.PlatformTenantSelfConsumerRevocationResponse{
		Consumers: consumers, RevokedKeys: result.RevokedKeys,
	})
}
