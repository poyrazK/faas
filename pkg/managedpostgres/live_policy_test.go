package managedpostgres

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestLivePolicyLaysOverTheFileAndRevertsWhenCleared(t *testing.T) {
	registry := testRegistry(t, &fakeProvider{capabilities: testCapabilities()}, func(c *Config) {
		c.ProvisioningEnabled = false
		c.Fleet = FleetConfig{MaxDatabases: 20, MaxMonthlyCostMillicents: 2_000_000}
	})
	if registry.ProvisioningEnabled() || registry.MaxDatabasesPerAccount() != 2 || registry.Fleet().MaxDatabases != 20 {
		t.Fatalf("file values not served: provisioning=%v per-account=%d fleet=%+v",
			registry.ProvisioningEnabled(), registry.MaxDatabasesPerAccount(), registry.Fleet())
	}

	backend := registry.backends["primary-a"]
	approvedAt := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	raw := json.RawMessage(`{"provisioning_enabled": true, "max_databases_per_account": 10,
		"fleet": {"max_databases": 80, "max_monthly_cost_millicents": 50000000},
		"production_approval": {"backend_id": "primary-a", "backend_fingerprint": "` + backend.Fingerprint + `",
			"approved_at": "2026-11-21T10:00:00Z", "until": "2027-02-19T10:00:00Z"}}`)
	if err := registry.ApplyLivePolicyJSON(raw); err != nil {
		t.Fatal(err)
	}
	if !registry.ProvisioningEnabled() || registry.MaxDatabasesPerAccount() != 10 {
		t.Fatalf("live values not served: provisioning=%v per-account=%d", registry.ProvisioningEnabled(), registry.MaxDatabasesPerAccount())
	}
	if fleet := registry.Fleet(); fleet.MaxDatabases != 80 || fleet.MaxMonthlyCostMillicents != 50_000_000 || fleet.PaidReserve != 16 {
		t.Fatalf("fleet = %+v, want 80 databases, $500 and the default 20%% paid reserve", fleet)
	}
	approval := registry.ProductionApproval(ProductionApproval{BackendID: "from-env"})
	if approval.BackendID != "primary-a" || !approval.ApprovedAt.Equal(approvedAt) {
		t.Fatalf("live approval not served: %+v", approval)
	}

	// Clearing the policy goes back to the file and the environment, not to
	// the previous live value.
	if err := registry.ApplyLivePolicyJSON(json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if registry.ProvisioningEnabled() || registry.MaxDatabasesPerAccount() != 2 || registry.Fleet().MaxDatabases != 20 {
		t.Fatal("clearing the live policy did not restore the file values")
	}
	if got := registry.ProductionApproval(ProductionApproval{BackendID: "from-env"}); got.BackendID != "from-env" {
		t.Fatalf("approval fallback = %q, want the environment's", got.BackendID)
	}
}

func TestLivePolicyUsageOverlayKeepsMeteringFromTheFile(t *testing.T) {
	registry := testRegistry(t, &fakeProvider{capabilities: testCapabilities()}, func(c *Config) {
		c.Usage = UsageConfig{Enabled: true, CollectionIntervalSeconds: 300, WindowSeconds: 3600, StaleAfterSeconds: 10800,
			MaxMonthlyCostMillicents: 2_000_000, MaxMonthlyComputeUnitSeconds: 2_700_000, MaxMonthlyStorageByteSeconds: 1 << 50,
			MaxMonthlyEgressBytes: 1 << 40, ComputeUnitHourMillicents: 10_600}
	})
	if err := registry.ApplyLivePolicyJSON(json.RawMessage(`{"usage": {"max_monthly_cost_millicents": 50000000, "compute_unit_hour_millicents": 14000}}`)); err != nil {
		t.Fatal(err)
	}
	usage := registry.UsagePolicy()
	if !usage.Enabled || usage.Window != time.Hour || usage.MaxMonthlyCostMillicents != 50_000_000 ||
		usage.ComputeUnitHourMillicents != 14_000 || usage.MaxMonthlyComputeUnitSeconds != 2_700_000 {
		t.Fatalf("usage = %+v: want metering and untouched ceilings from the file, cost cap and CU rate from the live policy", usage)
	}
}

func TestLivePolicyRefusalsKeepTheCurrentPolicy(t *testing.T) {
	registry := testRegistry(t, &fakeProvider{capabilities: testCapabilities()}, nil)
	backend := registry.backends["primary-a"]
	if err := registry.ApplyLivePolicyJSON(json.RawMessage(`{"max_databases_per_account": 5}`)); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct{ raw, want string }{
		"unknown field":         {`{"provisioning": true}`, "unknown field"},
		"metering is file-only": {`{"usage": {"enabled": false}}`, "unknown field"},
		"per-account bounds":    {`{"max_databases_per_account": 0}`, "1..100"},
		"negative rate":         {`{"usage": {"egress_gib_millicents": -1}}`, "must not be negative"},
		"reserve at the cap":    {`{"fleet": {"max_databases": 10, "max_monthly_cost_millicents": 1, "paid_reserve_databases": 10}}`, "fleet"},
		"short fingerprint":     {`{"production_approval": {"backend_id": "primary-a", "backend_fingerprint": "abc", "approved_at": "2026-11-21T10:00:00Z", "until": "2026-11-28T10:00:00Z"}}`, "64 lowercase hex"},
		"window over 90 days":   {`{"production_approval": {"backend_id": "primary-a", "backend_fingerprint": "` + backend.Fingerprint + `", "approved_at": "2026-11-21T10:00:00Z", "until": "2027-03-01T10:00:00Z"}}`, "at most 90 days"},
		"unknown backend":       {`{"production_approval": {"backend_id": "nope", "backend_fingerprint": "` + backend.Fingerprint + `", "approved_at": "2026-11-21T10:00:00Z", "until": "2026-11-28T10:00:00Z"}}`, "unknown backend"},
	} {
		err := registry.ApplyLivePolicyJSON(json.RawMessage(tc.raw))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
		if registry.MaxDatabasesPerAccount() != 5 {
			t.Fatalf("%s: a refused policy replaced the current one", name)
		}
	}
}
