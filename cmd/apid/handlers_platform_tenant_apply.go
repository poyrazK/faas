package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) applyPlatformTenant(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := s.platformTenantStore(w, acct); !ok {
		return
	}
	store, ok := s.store.(state.PlatformTenantApplyStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant onboarding is unavailable"))
		return
	}
	var req api.ApplyPlatformTenantRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	in, valid := platformTenantApplyInput(acct, req)
	if len(req.Surfaces) > 0 && !s.runtimeBool(runtimeConfigTenantSurfaces, api.TenantSurfacesEnabled()) {
		api.WriteProblem(w, api.ErrTenantSurfacesNotEnabled())
		return
	}
	if !valid {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid onboarding bundle", "customer and consumer names/references, app UUIDs, and surface UUIDs must be valid and unique"))
		return
	}
	result, err := store.ApplyPlatformTenant(r.Context(), in)
	if err != nil {
		s.platformTenantApplyError(w, err, acct.Plan)
		return
	}
	if !req.DryRun && platformTenantApplyChanged(result) {
		s.audit.Emit(r.Context(), "platform_tenant.applied", &acct.ID, map[string]any{
			"tenant_id": result.Tenant.ID, "external_ref": result.Tenant.ExternalRef,
			"consumers": len(result.Consumers), "surfaces": len(result.Surfaces),
		})
	}
	writeJSON(w, http.StatusOK, platformTenantApplyResponse(result, req.DryRun))
}

func platformTenantApplyInput(acct state.Account, req api.ApplyPlatformTenantRequest) (state.ApplyPlatformTenantParams, bool) {
	in := state.ApplyPlatformTenantParams{AccountID: acct.ID, ExternalRef: strings.TrimSpace(req.ExternalRef),
		Name: strings.TrimSpace(req.Name), DryRun: req.DryRun,
		TenantLimit: acct.Plan.ConsumerKeysPerAccount(), SurfaceIDs: req.SurfaceIDs}
	in.Limits, _ = api.LimitsFor(acct.Plan)
	if in.ExternalRef == "" || len(in.ExternalRef) > 256 || in.Name == "" || len(in.Name) > 128 {
		return in, false
	}
	seenConsumers := make(map[string]bool, len(req.Consumers))
	for _, consumer := range req.Consumers {
		consumer.ExternalRef, consumer.Name = strings.TrimSpace(consumer.ExternalRef), strings.TrimSpace(consumer.Name)
		appID, err := uuid.Parse(consumer.AppID)
		if err != nil || consumer.ExternalRef == "" ||
			len(consumer.ExternalRef) > 256 || consumer.Name == "" || len(consumer.Name) > 128 {
			return in, false
		}
		key := appID.String() + "\x00" + consumer.ExternalRef
		if seenConsumers[key] {
			return in, false
		}
		seenConsumers[key] = true
		in.Consumers = append(in.Consumers, state.ApplyPlatformTenantConsumer{
			AppID: consumer.AppID, ExternalRef: consumer.ExternalRef, Name: consumer.Name})
	}
	seenSurfaces := make(map[string]bool, len(req.SurfaceIDs))
	for _, id := range req.SurfaceIDs {
		surfaceID, err := uuid.Parse(id)
		if err != nil || seenSurfaces[surfaceID.String()] {
			return in, false
		}
		seenSurfaces[surfaceID.String()] = true
	}
	for _, wanted := range req.Surfaces {
		certKind := state.CertKind(wanted.CertKind)
		if certKind == "" {
			certKind = state.CertKindPerHostSAN
		}
		surface := state.ApplyPlatformTenantSurface{AppID: wanted.AppID, Name: strings.TrimSpace(wanted.Name), CertKind: certKind}
		for _, host := range wanted.Hostnames {
			surface.Hostnames = append(surface.Hostnames, state.ApplyPlatformTenantHostname{
				Hostname: strings.ToLower(strings.TrimSpace(host)), ChallengeToken: randomToken(16)})
		}
		in.Surfaces = append(in.Surfaces, surface)
	}
	return in, true
}

func (s *server) platformTenantApplyError(w http.ResponseWriter, err error, plan api.Plan) {
	var quota *state.PlatformTenantQuotaError
	var surfaceQuota *state.TenantSurfaceQuotaError
	var hostnameQuota *state.TenantHostnameQuotaError
	switch {
	case errors.As(err, &surfaceQuota):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "tenant_surface_quota", "Tenant surface quota reached", "this account has reached its plan's tenant surface limit"))
	case errors.As(err, &hostnameQuota):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "tenant_hostname_quota", "Tenant hostname quota reached", "this surface has reached its plan's hostname limit"))
	case errors.Is(err, state.ErrTenantSurfacesNotAllowed):
		api.WriteProblem(w, api.ErrTenantSurfacesNotAllowed(plan))
	case errors.As(err, &quota):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "platform_tenant_quota",
			"Platform tenant quota reached", "this account has reached its plan's platform tenant limit"))
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "no such app or tenant surface")
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
			"Onboarding conflict", "a customer or resource has a different name, is revoked, or belongs to another platform tenant"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid onboarding bundle", "consumer and surface references, surface names, and hostnames must be unique and valid"))
	default:
		api.WriteProblem(w, api.ErrInternal("could not apply platform tenant onboarding"))
	}
}

func platformTenantApplyChanged(result state.ApplyPlatformTenantResult) bool {
	if result.Action == "create" {
		return true
	}
	for _, item := range result.Consumers {
		if item.Action != "unchanged" {
			return true
		}
	}
	for _, item := range result.Surfaces {
		if item.Action != "unchanged" {
			return true
		}
		for _, host := range item.Hostnames {
			if host.Action != "unchanged" {
				return true
			}
		}
	}
	return false
}

func platformTenantApplyResponse(result state.ApplyPlatformTenantResult, dryRun bool) api.ApplyPlatformTenantResponse {
	out := api.ApplyPlatformTenantResponse{TenantID: result.Tenant.ID, ExternalRef: result.Tenant.ExternalRef,
		Name: result.Tenant.Name, Status: result.Tenant.Status, Action: result.Action, DryRun: dryRun,
		Consumers: make([]api.ApplyPlatformTenantConsumerResponse, 0, len(result.Consumers)),
		Surfaces:  make([]api.ApplyPlatformTenantSurfaceResponse, 0, len(result.Surfaces))}
	for _, item := range result.Consumers {
		c := item.Consumer
		out.Consumers = append(out.Consumers, api.ApplyPlatformTenantConsumerResponse{
			ID: c.ID, AppID: c.AppID, ExternalRef: c.ExternalRef, Name: c.Name,
			Status: string(c.Status), Action: item.Action})
	}
	for _, item := range result.Surfaces {
		s := item.Surface
		row := api.ApplyPlatformTenantSurfaceResponse{
			ID: s.ID, AppID: s.AppID, Name: s.Name, Status: string(s.Status),
			CertState: string(s.CertState), Action: item.Action}
		for _, host := range item.Hostnames {
			response := hostnameResponse(host.Hostname)
			if dryRun && host.Action == "create" {
				response.ChallengeToken = ""
			}
			row.Hostnames = append(row.Hostnames, api.ApplyPlatformTenantHostnameResponse{TenantHostnameResponse: response, Action: host.Action})
		}
		out.Surfaces = append(out.Surfaces, row)
	}
	return out
}
