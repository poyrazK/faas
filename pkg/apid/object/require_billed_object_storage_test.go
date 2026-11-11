package object

// Billing launch review 2026-11-10 (B3): paid plans store past their included
// object storage up to the plan ceiling whatever the Polar
// object_storage_billing_mode says, so production refuses to bill customers
// while that overage would never be charged.

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

type objectStorageSinkProvider struct {
	billing.Provider
	policy  billing.ObjectStorageLineItemPolicy
	enabled bool
}

func (p objectStorageSinkProvider) ObjectStorageLineItemPolicy() (billing.ObjectStorageLineItemPolicy, bool) {
	return p.policy, p.enabled
}

func (objectStorageSinkProvider) PublishObjectStorageCharge(context.Context, billing.ObjectStorageChargeStep) error {
	return nil
}

func registryWithBackend(t *testing.T) *objectstorage.Registry {
	t.Helper()
	policy := api.ObjectStoragePolicy{MaxAccountBytes: 1000, MaxBucketBytes: 1000, MaxAccountKeys: 100, MaxMonthlyCostMillicents: 100, MaxMonthlyRequests: 1000, MaxMonthlyEgressBytes: 1000, MaxMonthlyAuthorizations: 1000, MaxReportAgeSeconds: 3600}
	registry, err := objectstorage.NewRegistry(objectstorage.Config{Accounting: &policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "external"}, Backends: []objectstorage.BackendConfig{{ID: "external", Driver: "fake", Region: "us-east-1", Namespace: "isolated", Endpoint: "https://s3.example.test", S3Region: "us-east-1", UsageReportsPath: "/var/spool/faas/external-usage.json"}}}, func(string) string { return "" }, map[string]objectstorage.Factory{"fake": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) {
		return &listingProvider{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func TestRequireBilledObjectStorage(t *testing.T) {
	live := objectStorageSinkProvider{policy: billing.ObjectStorageLineItemPolicy{Mode: billing.MeterDeliveryLive}, enabled: true}
	shadow := objectStorageSinkProvider{policy: billing.ObjectStorageLineItemPolicy{Mode: billing.MeterDeliveryShadow}, enabled: true}
	off := objectStorageSinkProvider{}
	registry := registryWithBackend(t)
	for _, tc := range []struct {
		name       string
		provider   billing.Provider
		registry   *objectstorage.Registry
		production bool
		refused    bool
	}{
		{"production, overage off", off, registry, true, true},
		{"production, overage shadow", shadow, registry, true, true},
		{"production, overage live", live, registry, true, false},
		{"production, no object storage", off, nil, true, false},
		{"production, provider without object storage billing", nil, registry, true, false},
		{"staging, overage off", off, registry, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := RequireBilledObjectStorage(tc.provider, tc.registry, tc.production)
			if got := errors.Is(err, ErrObjectStorageOverageUnbilled); got != tc.refused {
				t.Fatalf("err = %v, refused = %v, want %v", err, got, tc.refused)
			}
		})
	}
}
