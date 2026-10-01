// adr: 375
package state

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func trafficPlannedTenantID(kind, identity string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("gregale.traffic.planned."+kind+"\x00"+identity)).String()
}

func trafficTenantApplyProposal(in ApplyPlatformTenantParams, result ApplyPlatformTenantResult) memTrafficPolicyChange {
	change := memTrafficPolicyChange{TenantSurfaces: make(map[string]TenantSurface), TenantHostnames: make(map[string]TenantHostname), PlatformTenants: make(map[string]PlatformTenant), TenantSurfaceLinks: make(map[string]string)}
	tenant := result.Tenant
	if tenant.ID == "" {
		tenant.ID = trafficPlannedTenantID("tenant", in.AccountID+"\x00"+in.ExternalRef)
	}
	change.PlatformTenants[tenant.ID] = tenant
	for _, item := range result.Surfaces {
		surface := item.Surface
		if surface.ID == "" {
			surface.ID = trafficPlannedTenantID("surface", in.AccountID+"\x00"+surface.Name)
		}
		change.TenantSurfaces[surface.ID] = surface
		change.TenantSurfaceLinks[surface.ID] = tenant.ID
		for _, wanted := range item.Hostnames {
			if wanted.Action != "create" {
				continue
			}
			host := wanted.Hostname
			if host.ID == "" {
				host.ID = trafficPlannedTenantID("hostname", strings.ToLower(host.Hostname))
			}
			host.SurfaceID = surface.ID
			change.TenantHostnames[host.ID] = host
		}
	}
	return change
}

func addTrafficTenantRemovals(change *memTrafficPolicyChange, changes []api.PlatformTenantReconciliationPlanChange) {
	for _, item := range changes {
		if item.Action != "remove_candidate" {
			continue
		}
		switch item.ResourceType {
		case "surface":
			change.TenantSurfaceLinks[item.ID] = ""
		case "hostname":
			change.TenantHostnames[item.ID] = TenantHostname{}
		}
	}
}

func trafficTenantApplyHosts(in ApplyPlatformTenantParams) []string {
	var hosts []string
	for _, surface := range in.Surfaces {
		for _, host := range surface.Hostnames {
			hosts = append(hosts, host.Hostname)
		}
	}
	return hosts
}

func projectTrafficTenantClaims(ctx context.Context, before trafficBindingClaims, change memTrafficPolicyChange) (trafficBindingClaims, error) {
	after := trafficBindingClaims{Domains: before.Domains}
	seen := make(map[string]bool)
	project := func(claim trafficTenantClaim) {
		if surface, found := change.TenantSurfaces[claim.Surface]; found {
			claim.Public = claim.Public && claim.App == surface.AppID && claim.Account == surface.AccountID
			claim.App, claim.Account, claim.Status = surface.AppID, surface.AccountID, surface.Status
		}
		if tenant, found := change.TenantSurfaceLinks[claim.Surface]; found {
			if tenant != claim.PlatformTenant {
				claim.Suspended = false
			}
			claim.PlatformTenant = tenant
		}
		if tenant, found := change.PlatformTenants[claim.PlatformTenant]; found {
			claim.Suspended = tenant.Status == PlatformTenantSuspended
		}
		after.Tenants = append(after.Tenants, claim)
	}
	for _, claim := range before.Tenants {
		if err := ctx.Err(); err != nil {
			return after, err
		}
		seen[claim.ID] = true
		if host, found := change.TenantHostnames[claim.ID]; found {
			if host.Hostname == "" {
				continue
			}
			if host.SurfaceID != claim.Surface {
				return after, ErrInvalidArgument
			}
			claim.Host, claim.Verified = strings.ToLower(host.Hostname), host.Verified()
		}
		project(claim)
	}
	for id, host := range change.TenantHostnames {
		if err := ctx.Err(); err != nil {
			return after, err
		}
		if seen[id] || host.Hostname == "" {
			continue
		}
		// Bulk-created hostnames are pending. A verified new binding requires
		// an authoritative app projection, supplied by the actual write read.
		if host.Verified() {
			return after, ErrInvalidArgument
		}
		surface := change.TenantSurfaces[host.SurfaceID]
		project(trafficTenantClaim{trafficHostTenant: trafficHostTenant{Host: strings.ToLower(host.Hostname), ID: host.ID, Surface: host.SurfaceID, App: surface.AppID}, Account: surface.AccountID, Status: surface.Status})
	}
	sort.Slice(after.Tenants, func(i, j int) bool { return after.Tenants[i].Host < after.Tenants[j].Host })
	if err := checkMemTrafficAnalysisInputs(len(after.Domains) + len(after.Tenants)); err != nil {
		return after, err
	}
	encoded, err := json.Marshal(after)
	if err != nil {
		return after, err
	}
	if size := int64(len(encoded)); size > api.TrafficPolicyMaxAnalysisMetadataBytes {
		return after, analysisLimit("metadata", "bytes", api.TrafficPolicyMaxAnalysisMetadataBytes, size)
	}
	return after, nil
}

func globalTrafficTenantProposal(ctx context.Context, before trafficHostAnalysis, claims trafficBindingClaims) (trafficHostAnalysis, error) {
	after := before
	after.Reservations = nil
	for _, claim := range before.Reservations {
		if claim.Kind != "tenant" {
			after.Reservations = append(after.Reservations, claim)
		}
	}
	for _, claim := range claims.Tenants {
		if err := ctx.Err(); err != nil {
			return after, err
		}
		after.Reservations = append(after.Reservations, trafficHostReservation{Kind: "tenant", Host: claim.Host})
	}
	sort.Slice(after.Reservations, func(i, j int) bool {
		a, b := after.Reservations[i], after.Reservations[j]
		return a.Kind < b.Kind || a.Kind == b.Kind && a.Host < b.Host
	})
	inputs := len(after.Groups) + len(after.Assets) + len(after.Environments) + len(after.PrimaryHosts) + len(after.AliasHosts) + len(after.Domains) + len(after.Tenants) + len(after.Reservations)
	if err := checkMemTrafficAnalysisInputs(inputs); err != nil {
		return after, err
	}
	encoded, err := json.Marshal(after)
	if err != nil {
		return after, err
	}
	if size := int64(len(encoded)); size > api.TrafficPolicyMaxAnalysisMetadataBytes {
		return after, analysisLimit("metadata", "bytes", api.TrafficPolicyMaxAnalysisMetadataBytes, size)
	}
	return after, nil
}

func (tx *trafficTenantBindingTx) ValidateProposal(ctx context.Context, change memTrafficPolicyChange) error {
	return boundedTrafficPolicyAnalysis(ctx, func(bounded context.Context) error {
		after, err := projectTrafficTenantClaims(bounded, tx.before, change)
		if err != nil {
			return err
		}
		globalAfter, err := globalTrafficTenantProposal(bounded, tx.globalBefore, after)
		if err != nil {
			return err
		}
		return tx.validate(bounded, after, globalAfter, tx.beforeViews)
	})
}

func trafficTenantOffboardingProposal(snapshot platformTenantOffboardingSnapshot) memTrafficPolicyChange {
	change := memTrafficPolicyChange{PlatformTenants: map[string]PlatformTenant{snapshot.TenantID: {
		ID: snapshot.TenantID, AccountID: snapshot.AccountID, Status: PlatformTenantSuspended}},
		TenantHostnames: make(map[string]TenantHostname), TenantSurfaceLinks: make(map[string]string)}
	for _, surface := range snapshot.Surfaces {
		if surface.Managed {
			change.TenantSurfaceLinks[surface.ID] = ""
		}
	}
	for _, host := range snapshot.Hostnames {
		if host.Managed {
			change.TenantHostnames[host.ID] = TenantHostname{}
		}
	}
	return change
}
