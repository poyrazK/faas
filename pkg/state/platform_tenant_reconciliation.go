package state

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrPlatformTenantPlanStale = errors.New("state: platform tenant reconciliation plan is stale")

type platformTenantReconciliationSnapshot struct {
	planned  ApplyPlatformTenantResult
	changes  []api.PlatformTenantReconciliationPlanChange
	planHash string
}

func validatePlatformTenantReconciliation(in PlatformTenantReconciliationParams) error {
	if in.TenantID == "" {
		return ErrInvalidArgument
	}
	return validatePlatformTenantApply(in.ApplyPlatformTenantParams)
}

func platformTenantReconciliationPlanChanges(
	in ApplyPlatformTenantParams,
	planned ApplyPlatformTenantResult,
	currentConsumers []APIConsumer,
	currentSurfaces []TenantSurface,
	hostnamesBySurface map[string][]TenantHostname,
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
			change.ManagedByPlatformTenant = platformTenantBoolPointer(item.Consumer.PlatformTenantManaged)
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
			ManagedByPlatformTenant: platformTenantBoolPointer(consumer.PlatformTenantManaged)})
	}

	desiredSurfaceIDs := make(map[string]bool, len(planned.Surfaces))
	for i, item := range planned.Surfaces {
		change := api.PlatformTenantReconciliationPlanChange{ResourceType: "surface", Action: platformTenantPlanAction(item.Action),
			ID: item.Surface.ID, AppID: item.Surface.AppID, Name: item.Surface.Name}
		if item.Action != "create" {
			change.ManagedByPlatformTenant = platformTenantBoolPointer(item.Surface.PlatformTenantManaged)
		}
		if item.Surface.ID != "" {
			desiredSurfaceIDs[canonicalPlatformTenantUUID(item.Surface.ID)] = true
		}
		changes = append(changes, change)

		// SurfaceIDs links an existing surface without declaring its hostname
		// set. Only Surfaces makes hostnames declarative.
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
				hostChange.ManagedByPlatformTenant = platformTenantBoolPointer(managed)
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
				ManagedByPlatformTenant: platformTenantBoolPointer(host.PlatformTenantManaged)})
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
			ManagedByPlatformTenant: platformTenantBoolPointer(surface.PlatformTenantManaged)})
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

func platformTenantBoolPointer(value bool) *bool { return &value }

func platformTenantReconciliationPlanHash(accountID, tenantID string, in ApplyPlatformTenantParams, changes []api.PlatformTenantReconciliationPlanChange) string {
	type consumerSpec struct {
		AppID, ExternalRef, Name string
	}
	type surfaceSpec struct {
		AppID, Name, CertKind string
		Hostnames             []string
	}
	desiredConsumers := make([]consumerSpec, 0, len(in.Consumers))
	for _, consumer := range in.Consumers {
		desiredConsumers = append(desiredConsumers, consumerSpec{AppID: canonicalPlatformTenantUUID(consumer.AppID),
			ExternalRef: consumer.ExternalRef, Name: consumer.Name})
	}
	sort.Slice(desiredConsumers, func(i, j int) bool {
		if desiredConsumers[i].AppID != desiredConsumers[j].AppID {
			return desiredConsumers[i].AppID < desiredConsumers[j].AppID
		}
		if desiredConsumers[i].ExternalRef != desiredConsumers[j].ExternalRef {
			return desiredConsumers[i].ExternalRef < desiredConsumers[j].ExternalRef
		}
		return desiredConsumers[i].Name < desiredConsumers[j].Name
	})
	desiredSurfaceIDs := make([]string, 0, len(in.SurfaceIDs))
	for _, id := range in.SurfaceIDs {
		desiredSurfaceIDs = append(desiredSurfaceIDs, canonicalPlatformTenantUUID(id))
	}
	sort.Strings(desiredSurfaceIDs)
	desiredSurfaces := make([]surfaceSpec, 0, len(in.Surfaces))
	for _, surface := range in.Surfaces {
		hosts := make([]string, 0, len(surface.Hostnames))
		for _, host := range surface.Hostnames {
			hosts = append(hosts, strings.ToLower(host.Hostname))
		}
		sort.Strings(hosts)
		desiredSurfaces = append(desiredSurfaces, surfaceSpec{AppID: canonicalPlatformTenantUUID(surface.AppID),
			Name: surface.Name, CertKind: string(surface.CertKind), Hostnames: hosts})
	}
	sort.Slice(desiredSurfaces, func(i, j int) bool {
		if desiredSurfaces[i].AppID != desiredSurfaces[j].AppID {
			return desiredSurfaces[i].AppID < desiredSurfaces[j].AppID
		}
		if desiredSurfaces[i].Name != desiredSurfaces[j].Name {
			return desiredSurfaces[i].Name < desiredSurfaces[j].Name
		}
		return desiredSurfaces[i].CertKind < desiredSurfaces[j].CertKind
	})
	payload := struct {
		Version     int
		AccountID   string
		TenantID    string
		ExternalRef string
		TenantName  string
		TenantLimit int
		Limits      api.Limits
		Consumers   []consumerSpec
		SurfaceIDs  []string
		Surfaces    []surfaceSpec
		CurrentPlan []api.PlatformTenantReconciliationPlanChange
	}{1, accountID, tenantID, in.ExternalRef, in.Name, in.TenantLimit, in.Limits,
		desiredConsumers, desiredSurfaceIDs, desiredSurfaces, changes}
	encoded, _ := json.Marshal(payload)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func validPlatformTenantPlanHash(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

func platformTenantPlanHashMatches(expected, actual string) bool {
	if !validPlatformTenantPlanHash(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) == 1
}

func platformTenantAppliedReconciliationChanges(changes []api.PlatformTenantReconciliationPlanChange, applied ApplyPlatformTenantResult) []api.PlatformTenantReconciliationPlanChange {
	consumers := make(map[string]ApplyPlatformTenantConsumerResult, len(applied.Consumers))
	for _, item := range applied.Consumers {
		consumers[platformTenantConsumerPlanKey(item.Consumer.AppID, item.Consumer.ExternalRef)] = item
	}
	surfacesByID := make(map[string]ApplyPlatformTenantSurfaceResult, len(applied.Surfaces))
	surfacesByName := make(map[string]ApplyPlatformTenantSurfaceResult, len(applied.Surfaces))
	hostnames := make(map[string]ApplyPlatformTenantHostnameResult)
	for _, item := range applied.Surfaces {
		if item.Surface.ID != "" {
			surfacesByID[canonicalPlatformTenantUUID(item.Surface.ID)] = item
		}
		surfacesByName[canonicalPlatformTenantUUID(item.Surface.AppID)+"\x00"+item.Surface.Name] = item
		for _, host := range item.Hostnames {
			hostnames[canonicalPlatformTenantUUID(item.Surface.ID)+"\x00"+strings.ToLower(host.Hostname.Hostname)] = host
		}
	}
	out := make([]api.PlatformTenantReconciliationPlanChange, len(changes))
	copy(out, changes)
	for i := range out {
		change := &out[i]
		switch change.ResourceType {
		case "consumer":
			if item, ok := consumers[platformTenantConsumerPlanKey(change.AppID, change.ExternalRef)]; ok {
				change.ID = item.Consumer.ID
				change.Action = appliedPlanAction(item.Action)
			}
		case "surface":
			item, ok := surfacesByID[canonicalPlatformTenantUUID(change.ID)]
			if !ok {
				item, ok = surfacesByName[canonicalPlatformTenantUUID(change.AppID)+"\x00"+change.Name]
			}
			if ok {
				change.ID = item.Surface.ID
				change.Action = appliedPlanAction(item.Action)
			}
		case "hostname":
			surfaceID := change.SurfaceID
			if surfaceID == "" {
				if item, ok := surfacesByName[canonicalPlatformTenantUUID(change.AppID)+"\x00"+change.Name]; ok {
					surfaceID = item.Surface.ID
					change.SurfaceID = surfaceID
				}
			}
			if item, ok := hostnames[canonicalPlatformTenantUUID(surfaceID)+"\x00"+strings.ToLower(change.Hostname)]; ok {
				change.ID = item.Hostname.ID
				change.Action = appliedPlanAction(item.Action)
			}
		}
		if change.Action == "remove_candidate" {
			if change.ResourceType == "hostname" {
				change.Action = "removed"
			} else {
				change.Action = "detached"
			}
		}
	}
	return out
}

func appliedPlanAction(action string) string {
	switch action {
	case "create":
		return "created"
	case "link":
		return "linked"
	case "unchanged":
		return "keep"
	default:
		return action
	}
}
