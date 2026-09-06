package objectstorage

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCalculateChargeRoundsEachComponentUp(t *testing.T) {
	pricing := api.ObjectStoragePricing{
		Currency:                     "EUR",
		StorageMillicentsPerGiBMonth: 1000,
		RequestsMillicentsPerMillion: 2000,
		EgressMillicentsPerGiB:       3000,
	}
	usage := api.ObjectStorageUsage{
		StoredByteHours: 1<<30*730 + 1,
		RequestCount:    1,
		EgressBytes:     1,
	}
	got, err := CalculateCharge(pricing, usage)
	if err != nil {
		t.Fatal(err)
	}
	if got.StorageMillicents != 1001 || got.RequestsMillicents != 1 || got.EgressMillicents != 1 || got.TotalMillicents != 1003 {
		t.Fatalf("unexpected charge: %+v", got)
	}
}

func TestCalculateChargeZeroUsage(t *testing.T) {
	got, err := CalculateCharge(api.ObjectStoragePricing{Currency: "EUR"}, api.ObjectStorageUsage{})
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalMillicents != 0 || got.Currency != "EUR" {
		t.Fatalf("unexpected zero charge: %+v", got)
	}
}

func TestCalculateChargeRejectsInvalidInput(t *testing.T) {
	if _, err := CalculateCharge(api.ObjectStoragePricing{Currency: "eur"}, api.ObjectStorageUsage{}); !errors.Is(err, ErrInvalidPricing) {
		t.Fatalf("invalid currency error = %v", err)
	}
	if _, err := CalculateCharge(api.ObjectStoragePricing{Currency: "EUR"}, api.ObjectStorageUsage{EgressBytes: -1}); !errors.Is(err, ErrInvalidPricing) {
		t.Fatalf("negative usage error = %v", err)
	}
}

func TestCalculateChargeDetectsOverflow(t *testing.T) {
	_, err := CalculateCharge(api.ObjectStoragePricing{Currency: "EUR", EgressMillicentsPerGiB: api.MaxObjectStoragePolicyValue}, api.ObjectStorageUsage{EgressBytes: api.MaxObjectStoragePolicyValue})
	if !errors.Is(err, ErrPricingOverflow) {
		t.Fatalf("overflow error = %v", err)
	}
}

func TestRegistryPricingIsOptionalAndValidated(t *testing.T) {
	backend := testBackend()
	base := Config{DefaultRegion: backend.Region, Defaults: map[string]string{backend.Region: backend.ID}, Backends: []BackendConfig{backend}}
	without, err := NewRegistry(base, testCredentials, map[string]Factory{"s3": NewS3})
	if err != nil || without.Pricing != nil {
		t.Fatalf("optional pricing: registry=%+v err=%v", without, err)
	}

	base.Pricing = &api.ObjectStoragePricing{Currency: "EUR", EgressMillicentsPerGiB: 1}
	with, err := NewRegistry(base, testCredentials, map[string]Factory{"s3": NewS3})
	if err != nil || with.Pricing == nil || with.Pricing.Currency != "EUR" {
		t.Fatalf("configured pricing: registry=%+v err=%v", with, err)
	}
	base.Pricing.Currency = "eur"
	if _, err := NewRegistry(base, testCredentials, map[string]Factory{"s3": NewS3}); err == nil {
		t.Fatal("invalid currency accepted")
	}
}
