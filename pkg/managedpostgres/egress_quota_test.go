// adr: 20261110160000 — slice 4: the cap sweep sets each provider object's
// egress quota to the per-database hold ceiling times the databases sharing
// it, a backstop that stops a database the sweep failed to hold.
package managedpostgres

import (
	"context"
	"errors"
	"maps"
	"math"
	"testing"
	"time"
)

type egressQuotaTestProvider struct {
	*capHoldTestProvider
	scope    map[string]string // resource -> egress quota scope
	quota    map[string]int64  // scope -> quota
	period   time.Time
	setErr   error
	setCalls map[string]int64 // resource -> quota written
}

func (p *egressQuotaTestProvider) ObserveEgressCap(_ context.Context, id string) (EgressCapObservation, error) {
	scope, ok := p.scope[id]
	if !ok {
		return EgressCapObservation{}, ErrNotFound
	}
	return EgressCapObservation{Scope: scope, PeriodStart: p.period, UsedBytes: 1, QuotaBytes: p.quota[scope]}, nil
}

func (p *egressQuotaTestProvider) SetEgressCap(_ context.Context, id string, quota int64) error {
	if p.setErr != nil {
		return p.setErr
	}
	p.setCalls[id] = quota
	p.quota[p.scope[id]] = quota
	return nil
}

func TestCapEnforcerSetsEgressQuotaBackstops(t *testing.T) {
	const gib = int64(1 << 30)
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 11, 5, 11, 22, 0, 0, time.UTC)
	errWrite := errors.New("quota write failed")
	for _, tc := range []struct {
		name        string
		mode        CapEnforcementMode
		limits      CapLimits
		allowance   EgressAllowance
		billingLive bool
		quota       map[string]int64
		period      time.Time
		setErr      error
		want        map[string]int64
		wantErr     error
	}{
		{name: "one quota per project, the ceiling for each database in it", mode: CapEnforcementOn,
			limits: CapLimits{Allowance: 5 * 3600, Ceiling: 10 * 3600}, allowance: EgressAllowance{CeilingBytes: 10 * gib},
			want: map[string]int64{"proj-a": 20 * gib, "proj-b": 10 * gib}},
		{name: "a subject without a compute ceiling still gets the backstop", mode: CapEnforcementOn,
			allowance: EgressAllowance{CeilingBytes: 10 * gib},
			want:      map[string]int64{"proj-a": 20 * gib, "proj-b": 10 * gib}},
		{name: "billed overage moves it to twice the allowance", mode: CapEnforcementOn, billingLive: true,
			allowance: EgressAllowance{CeilingBytes: 10 * gib, OverageLifted: true},
			want:      map[string]int64{"proj-a": 40 * gib, "proj-b": 20 * gib}},
		{name: "a right quota is left alone", mode: CapEnforcementOn,
			allowance: EgressAllowance{CeilingBytes: 10 * gib}, quota: map[string]int64{"a": 20 * gib},
			want: map[string]int64{"proj-b": 10 * gib}},
		{name: "shadow mode writes nothing", mode: CapEnforcementShadow,
			allowance: EgressAllowance{CeilingBytes: 10 * gib}},
		{name: "no allowance keeps the quota as it is", mode: CapEnforcementOn, quota: map[string]int64{"a": 7}},
		{name: "another period writes nothing", mode: CapEnforcementOn, period: month.AddDate(0, 0, 14),
			allowance: EgressAllowance{CeilingBytes: 10 * gib}},
		{name: "a failed write fails the subject", mode: CapEnforcementOn, setErr: errWrite,
			allowance: EgressAllowance{CeilingBytes: 10 * gib}, wantErr: errWrite},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observed := func(scope string) ComputeCapObservation {
				return ComputeCapObservation{Scope: scope, PeriodStart: month, UsedUnitSeconds: 600, QuotaUnitSeconds: 1}
			}
			period := tc.period
			if period.IsZero() {
				period = month
			}
			provider := &egressQuotaTestProvider{capHoldTestProvider: &capHoldTestProvider{capReportTestProvider: &capReportTestProvider{capTestProvider: &capTestProvider{
				fakeProvider: &fakeProvider{capabilities: testCapabilities()},
				observed:     map[string]ComputeCapObservation{"proj-a": observed("a"), "proj-a/br-restore": observed("a"), "proj-b": observed("b")},
				set:          map[string]int64{},
			}, reported: map[string]int64{}, from: map[string]time.Time{}}, held: map[string]bool{}, changes: map[string]bool{}},
				scope: map[string]string{"proj-a": "a", "proj-a/br-restore": "a", "proj-b": "b"},
				quota: map[string]int64{}, period: period, setErr: tc.setErr, setCalls: map[string]int64{}}
			maps.Copy(provider.quota, tc.quota)
			registry := testRegistry(t, provider, func(config *Config) {
				config.Usage = UsageConfig{Enabled: true, CollectionIntervalSeconds: 300, WindowSeconds: 3600,
					StaleAfterSeconds: 10800, MaxMonthlyCostMillicents: 1 << 40,
					MaxMonthlyComputeUnitSeconds: 1 << 40, MaxMonthlyStorageByteSeconds: 1 << 50, MaxMonthlyEgressBytes: 1 << 60}
				config.MaxDatabasesPerAccount = 3
			})
			backend, err := registry.Default("us-east-1")
			if err != nil {
				t.Fatal(err)
			}
			database := func(id, resource string, created time.Time) Database {
				return Database{ID: id, AccountID: "acct-a", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint,
					ProviderResourceID: resource, State: StateReady, Spec: testSpec(), CreatedAt: created}
			}
			store := &capEgressTestStore{capReportTestStore: &capReportTestStore{capTestStore: &capTestStore{
				databases: []Database{database("db-a", "proj-a", month), database("db-r", "proj-a/br-restore", month.Add(time.Minute)),
					database("db-b", "proj-b", month.Add(time.Hour))},
				ledger: map[string]int64{"acct-a": 3600},
			}}}
			enforcer, err := NewCapEnforcer(registry, store, CapEnforcerOptions{
				Notify: func(context.Context, CapEvent) error { return nil },
				Mode:   tc.mode, Now: func() time.Time { return now }, Qualification: testCapQualification(backend),
				EgressBillingLive: tc.billingLive,
				Ceiling:           func(context.Context, string) (CapLimits, error) { return tc.limits, nil },
				Egress:            func(context.Context, string, time.Time) (EgressAllowance, error) { return tc.allowance, nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			summary, err := enforcer.Enforce(context.Background())
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) || summary.EgressQuotasFailed == 0 {
					t.Fatalf("err %v, summary %+v; want %v", err, summary, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !maps.Equal(provider.setCalls, tc.want) && (len(provider.setCalls) != 0 || len(tc.want) != 0) {
				t.Fatalf("quotas written %v; want %v", provider.setCalls, tc.want)
			}
			if tc.mode == CapEnforcementShadow && summary.EgressQuotasShadowed != 2 {
				t.Fatalf("shadowed %d; want both projects", summary.EgressQuotasShadowed)
			}
			// Written quotas hold: the next sweep leaves them.
			clear(provider.setCalls)
			if _, err := enforcer.Enforce(context.Background()); err != nil || len(provider.setCalls) != 0 {
				t.Fatalf("second sweep wrote %v, %v", provider.setCalls, err)
			}
		})
	}
}

func TestEgressQuotaTarget(t *testing.T) {
	for _, tc := range []struct {
		ceiling   int64
		databases int
		want      int64
	}{{500, 1, 500}, {500, 3, 1500}, {0, 3, 0}, {500, 0, 0}, {math.MaxInt64 / 2, 3, math.MaxInt64}} {
		if got := egressQuotaTarget(tc.ceiling, tc.databases); got != tc.want {
			t.Errorf("egressQuotaTarget(%d, %d) = %d, want %d", tc.ceiling, tc.databases, got, tc.want)
		}
	}
}
