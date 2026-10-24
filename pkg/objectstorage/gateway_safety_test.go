package objectstorage

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 627, 20261024132844
func TestGatewaySafetyRegistryGates(t *testing.T) {
	since := time.Now().UTC().Add(-time.Minute)
	p := api.ObjectStoragePolicy{AccountingMode: api.ObjectStorageGatewaySafetyV1, GatewayMeteringSince: &since, MaxAccountBytes: 100, MaxBucketBytes: 100, MaxAccountKeys: 10, MaxMonthlyRequests: 100, MaxMonthlyEgressBytes: 100, MaxMonthlyAuthorizations: 100, MaxReportAgeSeconds: 60}
	b := testBackend()
	base := Config{Accounting: &p, PublicEndpoint: "https://s3.gregale.dev", Transfer: ObjectTransferConfig{Profile: "proxied"}, DefaultRegion: b.Region, Defaults: map[string]string{b.Region: b.ID}, Backends: []BackendConfig{b}}
	registry, err := NewRegistry(base, testCredentials, map[string]Factory{"s3": NewS3})
	if err != nil || !registry.CanProvision(b.Region) {
		t.Fatal("gateway mode required provider report path", err)
	}
	if charge, err := registry.ChargeForUsage(api.ObjectStorageUsage{}, api.ObjectStorageAllowance{}); err != nil || charge != nil {
		t.Fatal("gateway mode generated charge", charge, err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Config)
	}{
		{"request pricing", func(c *Config) {
			c.Pricing = &api.ObjectStoragePricing{Currency: "USD", RequestsMillicentsPerMillion: 1}
		}},
		{"egress pricing", func(c *Config) { c.Pricing = &api.ObjectStoragePricing{Currency: "USD", EgressMillicentsPerGiB: 1} }},
		{"direct uploads", func(c *Config) { c.Transfer.Profile = "direct" }},
		{"invalid gateway", func(c *Config) { c.PublicEndpoint = "://invalid" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := base
			tc.mutate(&candidate)
			if _, err := NewRegistry(candidate, testCredentials, map[string]Factory{"s3": NewS3}); err == nil {
				t.Fatal("unsafe configuration accepted")
			}
		})
	}
	if _, err := CalculateCharge(api.ObjectStoragePricing{Currency: "EUR"}, api.ObjectStorageUsage{UnavailableMeters: []string{"stored_byte_hours"}}, api.ObjectStorageAllowance{}); !errors.Is(err, ErrInvalidPricing) {
		t.Fatal("unknown meter treated as billable zero", err)
	}
	// ADR-1430's storage-only rate card bills Gregale's own storage meter.
	priced := base
	priced.Pricing = &api.ObjectStoragePricing{Currency: "USD", StorageMillicentsPerGiBMonth: api.ObjectStorageMillicentsPerGiBMonth}
	registry, err = NewRegistry(priced, testCredentials, map[string]Factory{"s3": NewS3})
	if err != nil {
		t.Fatal("storage-only rate card rejected", err)
	}
	charge, err := registry.ChargeForUsage(api.ObjectStorageUsage{UnavailableMeters: []string{"cost_millicents"}, StoredByteHours: 2 << 30 * api.ObjectStorageBillingHoursPerMonth}, api.ObjectStorageAllowance{})
	if err != nil || charge == nil || charge.StorageMillicents != 2*api.ObjectStorageMillicentsPerGiBMonth {
		t.Fatal("metered storage was not charged", charge, err)
	}
}
