package objectstorage

import (
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261101214152
// Production-us ran a 16 MiB pilot policy while plans promised up to
// 1,000 GiB; apid enforces the tighter of the two, so every plan was held to
// 16 MiB. In production such a policy closes provisioning.
func TestPlanShortfallsCloseProductionProvisioning(t *testing.T) {
	since := time.Now().UTC().Add(-time.Minute)
	most := api.ObjectStoragePlanLimitsMax()
	planSized := api.ObjectStoragePolicy{AccountingMode: api.ObjectStorageGatewaySafetyV1, GatewayMeteringSince: &since,
		MaxAccountBytes: most.MaxAccountBytes, MaxBucketBytes: most.MaxAccountBytes, MaxAccountKeys: api.MaxObjectStorageAccountKeys,
		MaxMonthlyRequests: 10_000_000, MaxMonthlyEgressBytes: most.MaxMonthlyEgressBytes, MaxMonthlyAuthorizations: 1_000_000, MaxReportAgeSeconds: 900}
	pilot := planSized
	pilot.MaxAccountBytes, pilot.MaxBucketBytes, pilot.MaxAccountKeys, pilot.MaxMonthlyEgressBytes = 16<<20, 8<<20, 100, 64<<20
	for _, tc := range []struct {
		name          string
		policy        api.ObjectStoragePolicy
		environment   string
		wantShortfall []string
		wantProvision bool
	}{
		{name: "plan-sized policy in production", policy: planSized, environment: "production", wantProvision: true},
		{name: "pilot policy in production", policy: pilot, environment: "production",
			wantShortfall: []string{"max_account_bytes_below_plan", "max_bucket_bytes_below_plan", "max_monthly_egress_bytes_below_plan", "max_account_keys_below_plan"}},
		{name: "pilot policy elsewhere", policy: pilot, environment: "staging",
			wantShortfall: []string{"max_account_bytes_below_plan", "max_bucket_bytes_below_plan", "max_monthly_egress_bytes_below_plan", "max_account_keys_below_plan"}, wantProvision: true},
		{name: "pilot policy without an environment", policy: pilot,
			wantShortfall: []string{"max_account_bytes_below_plan", "max_bucket_bytes_below_plan", "max_monthly_egress_bytes_below_plan", "max_account_keys_below_plan"}, wantProvision: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := tc.policy
			b := testBackend()
			c := Config{Accounting: &policy, PublicEndpoint: "https://s3.gregale.dev", Transfer: ObjectTransferConfig{Profile: "proxied"}, DefaultRegion: b.Region, Defaults: map[string]string{b.Region: b.ID}, Backends: []BackendConfig{b}}
			getenv := func(name string) string {
				if name == "FAAS_ENVIRONMENT" {
					return tc.environment
				}
				return testCredentials(name)
			}
			registry, err := NewRegistry(c, getenv, map[string]Factory{"s3": NewS3})
			if err != nil {
				t.Fatal(err)
			}
			if got := registry.PlanShortfalls(); !slices.Equal(got, tc.wantShortfall) {
				t.Fatalf("PlanShortfalls = %v, want %v", got, tc.wantShortfall)
			}
			if got := registry.CanProvisionAllRegions(); got != tc.wantProvision {
				t.Fatalf("CanProvisionAllRegions = %v, want %v", got, tc.wantProvision)
			}
		})
	}
	var none *Registry
	if none.PlanShortfalls() != nil {
		t.Fatal("a missing registry has no policy to fall short")
	}
}
