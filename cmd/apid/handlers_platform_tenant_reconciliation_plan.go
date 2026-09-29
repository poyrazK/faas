package main

import (
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) planPlatformTenantReconciliation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenantStore, ok := s.platformTenantStore(w, acct)
	if !ok {
		return
	}
	tenant, ok := s.platformTenantByPath(w, r, acct, tenantStore)
	if !ok {
		return
	}
	applyStore, ok := s.store.(state.PlatformTenantApplyStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant reconciliation is unavailable"))
		return
	}
	var req api.PlanPlatformTenantReconciliationRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if len(req.Surfaces) > 0 && !s.runtimeBool(runtimeConfigTenantSurfaces, api.TenantSurfacesEnabled()) {
		api.WriteProblem(w, api.ErrTenantSurfacesNotEnabled())
		return
	}
	applyReq := api.ApplyPlatformTenantRequest{ExternalRef: tenant.ExternalRef, Name: tenant.Name, DryRun: true,
		Consumers: req.Consumers, SurfaceIDs: req.SurfaceIDs, Surfaces: req.Surfaces}
	in, valid := platformTenantApplyInput(acct, applyReq)
	if !valid {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid reconciliation plan", "consumer and surface references, names, and hostnames must be valid and unique"))
		return
	}
	planned, err := applyStore.ApplyPlatformTenant(r.Context(), in)
	if err != nil {
		s.platformTenantApplyError(w, err, acct.Plan)
		return
	}
	if planned.Tenant.ID == "" || planned.Tenant.ID != tenant.ID {
		api.WriteProblem(w, api.ErrInternal("could not resolve platform tenant reconciliation plan"))
		return
	}
	consumers, err := tenantStore.ListPlatformTenantConsumers(r.Context(), acct.ID, tenant.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list platform tenant consumers for reconciliation"))
		return
	}
	surfaces, err := tenantStore.ListPlatformTenantSurfaces(r.Context(), acct.ID, tenant.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list platform tenant surfaces for reconciliation"))
		return
	}
	hostnamesBySurface := make(map[string][]state.TenantHostname)
	for _, surface := range planned.Surfaces {
		if surface.Surface.ID == "" {
			continue
		}
		if _, loaded := hostnamesBySurface[surface.Surface.ID]; loaded {
			continue
		}
		hostnames, err := s.store.ListTenantHostnamesForSurface(r.Context(), surface.Surface.ID)
		if err != nil {
			api.WriteProblem(w, api.ErrInternal("could not list tenant hostnames for reconciliation"))
			return
		}
		hostnamesBySurface[surface.Surface.ID] = hostnames
	}
	changes := platformTenantReconciliationPlanChanges(in, planned, consumers, surfaces, hostnamesBySurface)
	writeJSON(w, http.StatusOK, api.PlatformTenantReconciliationPlanResponse{TenantID: tenant.ID, Changes: changes})
}

func platformTenantReconciliationPlanChanges(
	in state.ApplyPlatformTenantParams,
	planned state.ApplyPlatformTenantResult,
	currentConsumers []state.APIConsumer,
	currentSurfaces []state.TenantSurface,
	hostnamesBySurface map[string][]state.TenantHostname,
) []api.PlatformTenantReconciliationPlanChange {
	changes := make([]api.PlatformTenantReconciliationPlanChange, 0,
		len(planned.Consumers)+len(planned.Surfaces)+len(currentConsumers)+len(currentSurfaces))
	desiredConsumers := make(map[string]bool, len(in.Consumers))
	for _, consumer := range in.Consumers {
		desiredConsumers[platformTenantConsumerPlanKey(consumer.AppID, consumer.ExternalRef)] = true
	}
	for _, item := range planned.Consumers {
		change := api.PlatformTenantReconciliationPlanChange{ResourceType: "consumer", Action: platformTenantPlanAction(item.Action),
			ID: item.Consumer.ID, AppID: item.Consumer.AppID, ExternalRef: item.Consumer.ExternalRef, Name: item.Consumer.Name}
		if item.Action != "create" {
			change.ManagedByPlatformTenant = boolPointer(item.Consumer.PlatformTenantManaged)
		}
		changes = append(changes, change)
	}
	for _, consumer := range currentConsumers {
		if desiredConsumers[platformTenantConsumerPlanKey(consumer.AppID, consumer.ExternalRef)] {
			continue
		}
		action := "retain_unmanaged"
		if consumer.PlatformTenantManaged {
			action = "remove_candidate"
		}
		changes = append(changes, api.PlatformTenantReconciliationPlanChange{ResourceType: "consumer", Action: action,
			ID: consumer.ID, AppID: consumer.AppID, ExternalRef: consumer.ExternalRef, Name: consumer.Name,
			ManagedByPlatformTenant: boolPointer(consumer.PlatformTenantManaged)})
	}

	desiredSurfaceIDs := make(map[string]bool, len(planned.Surfaces))
	for i, item := range planned.Surfaces {
		change := api.PlatformTenantReconciliationPlanChange{ResourceType: "surface", Action: platformTenantPlanAction(item.Action),
			ID: item.Surface.ID, AppID: item.Surface.AppID, Name: item.Surface.Name}
		if item.Action != "create" {
			change.ManagedByPlatformTenant = boolPointer(item.Surface.PlatformTenantManaged)
		}
		if item.Surface.ID != "" {
			desiredSurfaceIDs[canonicalPlatformTenantUUID(item.Surface.ID)] = true
			change.ID = item.Surface.ID
		}
		changes = append(changes, change)

		// A SurfaceID links an existing surface without declaring its hostname
		// set. Only a surface object in Surfaces makes hostnames declarative.
		if i < len(in.SurfaceIDs) {
			continue
		}
		wantedSurface := in.Surfaces[i-len(in.SurfaceIDs)]
		wantedHosts := make(map[string]bool, len(wantedSurface.Hostnames))
		for _, host := range wantedSurface.Hostnames {
			wantedHosts[strings.ToLower(host.Hostname)] = true
		}
		for _, host := range item.Hostnames {
			hostChange := api.PlatformTenantReconciliationPlanChange{ResourceType: "hostname", Action: platformTenantPlanAction(host.Action),
				AppID: item.Surface.AppID, Name: item.Surface.Name, SurfaceID: item.Surface.ID, Hostname: host.Hostname.Hostname}
			if host.Action != "create" {
				hostChange.ID = host.Hostname.ID
				managed := host.Hostname.PlatformTenantManaged
				for _, current := range hostnamesBySurface[item.Surface.ID] {
					if strings.EqualFold(current.Hostname, host.Hostname.Hostname) {
						hostChange.ID = current.ID
						managed = current.PlatformTenantManaged
						break
					}
				}
				hostChange.ManagedByPlatformTenant = boolPointer(managed)
			}
			changes = append(changes, hostChange)
		}
		for _, host := range hostnamesBySurface[item.Surface.ID] {
			if wantedHosts[strings.ToLower(host.Hostname)] {
				continue
			}
			action := "retain_unmanaged"
			if host.PlatformTenantManaged {
				action = "remove_candidate"
			}
			changes = append(changes, api.PlatformTenantReconciliationPlanChange{ResourceType: "hostname", Action: action,
				ID: host.ID, AppID: item.Surface.AppID, Name: item.Surface.Name, SurfaceID: item.Surface.ID, Hostname: host.Hostname,
				ManagedByPlatformTenant: boolPointer(host.PlatformTenantManaged)})
		}
	}
	for _, surface := range currentSurfaces {
		if desiredSurfaceIDs[canonicalPlatformTenantUUID(surface.ID)] {
			continue
		}
		action := "retain_unmanaged"
		if surface.PlatformTenantManaged {
			action = "remove_candidate"
		}
		changes = append(changes, api.PlatformTenantReconciliationPlanChange{ResourceType: "surface", Action: action,
			ID: surface.ID, AppID: surface.AppID, Name: surface.Name,
			ManagedByPlatformTenant: boolPointer(surface.PlatformTenantManaged)})
	}
	sort.Slice(changes, func(i, j int) bool {
		a, b := changes[i], changes[j]
		if a.ResourceType != b.ResourceType {
			return a.ResourceType < b.ResourceType
		}
		if a.AppID != b.AppID {
			return a.AppID < b.AppID
		}
		if a.ExternalRef != b.ExternalRef {
			return a.ExternalRef < b.ExternalRef
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.SurfaceID != b.SurfaceID {
			return a.SurfaceID < b.SurfaceID
		}
		if a.Hostname != b.Hostname {
			return a.Hostname < b.Hostname
		}
		if a.Action != b.Action {
			return a.Action < b.Action
		}
		return a.ID < b.ID
	})
	return changes
}

func platformTenantPlanAction(action string) string {
	if action == "unchanged" {
		return "keep"
	}
	return action
}

func platformTenantConsumerPlanKey(appID, externalRef string) string {
	return canonicalPlatformTenantUUID(appID) + "\x00" + externalRef
}

func canonicalPlatformTenantUUID(value string) string {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return strings.ToLower(value)
	}
	return parsed.String()
}

func boolPointer(value bool) *bool { return &value }
