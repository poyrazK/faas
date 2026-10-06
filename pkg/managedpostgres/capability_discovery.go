package managedpostgres

import "context"

// CapabilityContractVersion versions customer-visible PostgreSQL behavior.
// Provider identities and placement fingerprints never belong in this view.
const CapabilityContractVersion = 2

type CapabilityDiscovery struct {
	Region              string
	Capabilities        Capabilities
	DatabaseLimit       int
	ProvisioningEnabled bool
}

// DiscoverCapabilities is a provider-I/O-free view of the default placement.
// It describes support and the rollout gate, not current usage/budget admission.
func (s *Service) DiscoverCapabilities(ctx context.Context, accountID, region string) (CapabilityDiscovery, error) {
	if s == nil || accountID == "" {
		return CapabilityDiscovery{}, ErrInvalid
	}
	if region == "" {
		region = s.registry.DefaultRegion
	}
	backend, err := s.registry.Default(region)
	if err != nil {
		return CapabilityDiscovery{}, err
	}
	c := backend.Capabilities
	c.PostgresMajors = append([]int(nil), c.PostgresMajors...)
	c.ServiceClasses = append([]ServiceClass(nil), c.ServiceClasses...)
	c.Availability = append([]Availability(nil), c.Availability...)
	c.CredentialAccess = append([]CredentialAccess(nil), c.CredentialAccess...)
	c.UsageMeters = nil
	return CapabilityDiscovery{Region: region, Capabilities: c, DatabaseLimit: s.registry.MaxDatabasesPerAccount,
		ProvisioningEnabled: s.provisioningEnabled() && s.provisioningAllowed(ctx, accountID)}, nil
}
