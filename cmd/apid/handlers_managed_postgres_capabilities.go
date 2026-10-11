package main

import (
	"net/http"
	"net/url"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getManagedPostgresCapabilities(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) > 1 || (len(query) == 1 && len(query["region"]) != 1) || len(query.Get("region")) > 255 {
		managedPostgresProblem(w, managedpostgres.ErrInvalid)
		return
	}
	limits, ok := api.ManagedPostgresLimitsFor(api.Plan(acct.Plan))
	if !ok {
		managedPostgresPlanDenied(w, api.Plan(acct.Plan), "unknown plan")
		return
	}
	discovery, err := s.managedPostgres.DiscoverCapabilities(r.Context(), acct.ID, query.Get("region"))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, managedPostgresCapabilitiesView(discovery, limits))
}

func managedPostgresCapabilitiesView(d managedpostgres.CapabilityDiscovery, limits api.ManagedPostgresPlanLimits) api.ManagedPostgresCapabilities {
	c := d.Capabilities
	out := api.ManagedPostgresCapabilities{ContractVersion: managedpostgres.CapabilityContractVersion, Region: d.Region,
		DatabaseLimit: min(d.DatabaseLimit, limits.DatabasesMax), PostgresMajors: []int{}, ServiceClasses: []string{},
		Availability: []string{}, CredentialAccess: []string{}}
	if out.DatabaseLimit == 0 || (!c.ScaleToZero && !limits.AlwaysOnAllowed) {
		return out
	}
	out.PostgresMajors = append(out.PostgresMajors, c.PostgresMajors...)
	for _, class := range c.ServiceClasses {
		if (class == managedpostgres.ClassDevelopment && limits.DevelopmentAllowed) ||
			(class == managedpostgres.ClassBurstable && limits.BurstableAllowed) ||
			(class == managedpostgres.ClassProduction && limits.ProductionAllowed) {
			out.ServiceClasses = append(out.ServiceClasses, string(class))
		}
	}
	for _, availability := range c.Availability {
		out.Availability = append(out.Availability, string(availability))
	}
	for _, access := range c.CredentialAccess {
		out.CredentialAccess = append(out.CredentialAccess, string(access))
	}
	sort.Ints(out.PostgresMajors)
	sort.Strings(out.ServiceClasses)
	sort.Strings(out.Availability)
	sort.Strings(out.CredentialAccess)
	out.ScaleToZeroUpdate = c.ScaleToZeroUpdate && c.ScaleToZero && len(out.ServiceClasses) > 0
	out.ClassResize = c.ClassResize && len(out.ServiceClasses) > 1
	out.ProvisioningEnabled = d.ProvisioningEnabled && len(out.ServiceClasses) > 0
	out.ScaleToZero, out.AlwaysOn, out.PooledConnections = c.ScaleToZero, limits.AlwaysOnAllowed, c.PooledConnections
	out.StorageLimitBytes = limits.StorageLimitBytes
	if c.MaxStorageBytes > 0 {
		out.StorageLimitBytes = min(out.StorageLimitBytes, c.MaxStorageBytes)
	}
	if c.PointInTimeRestore {
		out.RestoreWindowSeconds = limits.RestoreWindowSeconds
		if c.MaxRestoreWindowSeconds > 0 {
			out.RestoreWindowSeconds = min(out.RestoreWindowSeconds, c.MaxRestoreWindowSeconds)
		}
	}
	out.PointInTimeRestore = out.RestoreWindowSeconds > 0
	return out
}
