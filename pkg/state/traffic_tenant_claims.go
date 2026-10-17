// adr: 570
package state

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type trafficTenantClaim struct {
	trafficHostTenant
	Account, AppAccount         string
	Status                      SurfaceStatus
	Verified, Public, Suspended bool
}

type trafficBindingClaims struct {
	Domains            []trafficDomainClaim
	Tenants            []trafficTenantClaim
	Aliases, Primaries []trafficNamedHostClaim
}

func (c trafficTenantClaim) eligible() bool {
	return c.Status == SurfaceStatusActive && c.Verified && c.Public && !c.Suspended
}

func (c trafficTenantClaim) blocked() bool {
	return c.Status != SurfaceStatusDeleted && c.Suspended
}

func tenantTrafficBinding(t trafficHostTenant) trafficHostDomain {
	return trafficHostDomain{Domain: t.Host, App: t.App, Tenant: t.Surface + "/" + t.ID + "/" + t.PlatformTenant}
}

func selectTrafficTenantBindings(bindings map[trafficHostDomain]*trafficHostEnvironment, claim *trafficTenantClaim, enabled, namespace bool) {
	selected := enabled && !namespace && claim != nil
	for binding := range bindings {
		if binding.Tenant != "" {
			if !selected || !claim.eligible() || binding != tenantTrafficBinding(claim.trafficHostTenant) {
				delete(bindings, binding)
			}
		} else if binding.Domain != "" && selected && (claim.eligible() || claim.blocked()) {
			delete(bindings, binding)
		}
	}
}

func readTrafficBindingClaims(ctx context.Context, reader sqlc.DBTX, appsSuffix string) (trafficBindingClaims, error) {
	bounded, cancel := context.WithTimeout(ctx, api.TrafficPolicyAnalysisSQLTimeout)
	defer cancel()
	row, err := sqlc.New().ReadTrafficBindingClaims(bounded, reader, sqlc.ReadTrafficBindingClaimsParams{
		MaxInputs: api.TrafficPolicyMaxAnalysisInputs, MaxBytes: api.TrafficPolicyMaxAnalysisMetadataBytes, AppsSuffix: appsSuffix,
	})
	if err != nil {
		if ctx.Err() != nil {
			return trafficBindingClaims{}, ctx.Err()
		}
		if bounded.Err() != nil {
			limit := api.TrafficPolicyAnalysisSQLTimeout.Milliseconds()
			return trafficBindingClaims{}, analysisLimit("database_time", "milliseconds", limit, limit+1)
		}
		return trafficBindingClaims{}, fmt.Errorf("state: read traffic binding metadata: %w", mapErr(err))
	}
	if row.Inputs > api.TrafficPolicyMaxAnalysisInputs {
		return trafficBindingClaims{}, analysisLimit("inputs", "bindings", api.TrafficPolicyMaxAnalysisInputs, row.Inputs)
	}
	if row.Bytes > api.TrafficPolicyMaxAnalysisMetadataBytes {
		return trafficBindingClaims{}, analysisLimit("metadata", "bytes", api.TrafficPolicyMaxAnalysisMetadataBytes, row.Bytes)
	}
	var claims trafficBindingClaims
	if err := json.Unmarshal(row.Data, &claims); err != nil {
		return trafficBindingClaims{}, fmt.Errorf("state: decode traffic binding metadata: %w", err)
	}
	return claims, nil
}

func trafficTenantOwnerView(view trafficHostAnalysis, domains []trafficDomainClaim, tenants []trafficTenantClaim, account string, enabled bool) trafficHostAnalysis {
	view = trafficDomainOwnerView(view, domains, account)
	view.Tenants = nil
	view.SelectTenants, view.TenantSurfaces, view.TenantClaims = true, enabled, tenants
	if enabled {
		for _, claim := range tenants {
			if claim.Account == account && claim.eligible() {
				view.Tenants = append(view.Tenants, claim.trafficHostTenant)
			}
		}
	}
	return view
}

func checkTrafficTenantBindingOwner(ctx context.Context, beforeView, afterView trafficHostAnalysis, beforeDomains, afterDomains []trafficDomainClaim, beforeTenants, afterTenants []trafficTenantClaim, account string, globalBefore, globalAfter trafficHostAnalysis) error {
	for _, enabled := range []bool{false, true} {
		prior := trafficTenantOwnerView(beforeView, beforeDomains, beforeTenants, account, enabled)
		next := trafficTenantOwnerView(afterView, afterDomains, afterTenants, account, enabled)
		prior.AllowGlobalRoutes, next.AllowGlobalRoutes = true, true
		prior.Reservations, next.Reservations = globalBefore.Reservations, globalAfter.Reservations
		if err := checkTrafficHostAnalysis(ctx, prior, next); err != nil {
			return err
		}
	}
	return nil
}
